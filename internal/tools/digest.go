package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func (h *Hub) registerDigestTools(s *server.MCPServer) {
	if !h.cfg.IsDisabled("get_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_channel_digest",
				mcp.WithDescription("Compact digest of recent messages from one channel or DM. Either give a relative `hours` window (default), or absolute `after`/`before` (YYYY-MM-DD) for post-mortem-style fetches."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops), a DM as @handle, a bare U… user id (as printed in unread-summary DM headers), or a canonical C/G/D conversation id")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24). Ignored when after/before are set.")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages to inline (default: 50)")),
				mcp.WithString("after", mcp.Description("Absolute lower bound, YYYY-MM-DD (UTC). Overrides hours when set.")),
				mcp.WithString("before", mcp.Description("Absolute upper bound, YYYY-MM-DD (UTC, exclusive day end). Pair with after for date ranges.")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
				mcp.WithBoolean("full_text", mcp.Description("Render message bodies in full instead of truncating long ones to a compact preview (default: false). Use when ingesting a channel verbatim — e.g. into a knowledge base.")),
				mcp.WithBoolean("with_replies", mcp.Description("Also fetch and inline thread replies for every thread in the window. Defaults per conversation kind: ON for DMs (a thread reply there IS the conversation) and OFF for channels (one conversations.replies call per thread). Set explicitly to override — true to expand a channel whose real content lives in threads, false for a leaner DM read.")),
				mcp.WithNumber("thread_preview_replies", mcp.Description("Max replies inlined per thread when with_replies=true (default: 10; pass a big number for full threads)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				scoped, _, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}
				hours := int(req.GetFloat("hours", float64(h.cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 50))
				after := req.GetString("after", "")
				before := req.GetString("before", "")
				fullText := req.GetBool("full_text", false)
				// with_replies defaults per conversation kind: ON for DMs
				// (few messages, and a thread reply there IS the
				// conversation — see ADR 064), OFF for channels, where
				// fan-out × threads would multiply API calls. An explicit
				// value always wins.
				withReplies := req.GetBool("with_replies", false)
				if _, explicit := req.GetArguments()["with_replies"]; !explicit {
					withReplies = isDMRef(channel)
				}
				replyCap := int(req.GetFloat("thread_preview_replies", 10))

				oldest, latest, err := parseRange(after, before, hours)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				txt, err := scoped.channelDigestRange(ctx, channel, oldest, latest, maxShow, fullText, withReplies, replyCap)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(txt), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_multi_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_multi_channel_digest",
				mcp.WithDescription("Compact digest across multiple channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 20)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgAll)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runMultiChannelDigest(ctx,
					req.GetString("workspace", ""),
					req.GetString("channels", ""),
					int(req.GetFloat("hours", float64(h.cfg.DigestHours))),
					int(req.GetFloat("max_messages", 20))), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_morning_recap") {
		s.AddTool(
			mcp.NewTool("get_morning_recap",
				mcp.WithDescription("Morning recap: decisions + channel activity across channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 15)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgAll)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runMorningRecap(ctx,
					req.GetString("workspace", ""),
					req.GetString("channels", ""),
					int(req.GetFloat("hours", float64(h.cfg.DigestHours))),
					int(req.GetFloat("max_messages", 15))), nil
			},
		)
	}
}

// runMultiChannelDigest fans the multi-channel digest across the
// requested workspaces — empty arg means every workspace, mirroring the
// unread sweep and list_channels. A single workspace (the common case,
// or an explicit label) renders the flat body byte-for-byte as before;
// two or more nest each under a `## [label]` heading. Per-workspace the
// channel set resolves against THAT workspace's config / joined
// channels, so `SLACK_CHANNELS` and auto-discovery stay workspace-local.
func (h *Hub) runMultiChannelDigest(ctx context.Context, workspace, channels string, hours, maxShow int) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	multi := len(targets) > 1

	var sections []string
	for _, ws := range targets {
		body, err := h.withClient(ws.Client).multiChannelDigestBody(ctx, channels, hours, maxShow)
		switch {
		case err != nil && !multi:
			return mcp.NewToolResultError(err.Error())
		case err != nil:
			sections = append(sections, workspaceSection(ws.Name, "_error: "+err.Error()+"_"))
		case !multi:
			return mcp.NewToolResultText(body)
		default:
			sections = append(sections, workspaceSection(ws.Name, body))
		}
	}
	return mcp.NewToolResultText(strings.Join(sections, "\n\n"))
}

// multiChannelDigestBody renders the digest for the single workspace
// this Hub is scoped to. Empty result-channel set is an error the
// caller surfaces (per workspace in the multi case).
func (h *Hub) multiChannelDigestBody(ctx context.Context, channels string, hours, maxShow int) (string, error) {
	list, _, err := h.resolveTargetChannels(ctx, channels)
	if err != nil {
		return "", fmt.Errorf("auto-discover channels: %w", err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no channels available — pass channels, set SLACK_CHANNELS, or join some channels")
	}
	var parts []string
	for _, ch := range list {
		txt, err := h.channelDigest(ctx, ch, hours, maxShow, false)
		if err != nil {
			parts = append(parts, fmt.Sprintf("## #%s\nerror: %v", ch, err))
			continue
		}
		parts = append(parts, txt)
	}
	return strings.Join(parts, "\n\n"), nil
}

// runMorningRecap is the get_morning_recap analogue of
// runMultiChannelDigest: same fan-out contract, but the per-workspace
// body carries its own Decisions + Activity subsections. The top
// "# Morning Recap" title is owned here so single-workspace output is
// unchanged and the multi case shows it once above the labelled
// sections.
func (h *Hub) runMorningRecap(ctx context.Context, workspace, channels string, hours, maxShow int) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	multi := len(targets) > 1
	title := fmt.Sprintf("# Morning Recap (last %dh)\n\n", hours)

	var sections []string
	for _, ws := range targets {
		body, err := h.withClient(ws.Client).morningRecapBody(ctx, channels, hours, maxShow)
		switch {
		case err != nil && !multi:
			return mcp.NewToolResultError(err.Error())
		case err != nil:
			sections = append(sections, workspaceSection(ws.Name, "_error: "+err.Error()+"_"))
		case !multi:
			return mcp.NewToolResultText(title + body)
		default:
			sections = append(sections, workspaceSection(ws.Name, body))
		}
	}
	return mcp.NewToolResultText(title + strings.Join(sections, "\n\n"))
}

// morningRecapBody renders the decisions + activity recap for the
// single workspace this Hub is scoped to, without the top-level title.
func (h *Hub) morningRecapBody(ctx context.Context, channels string, hours, maxShow int) (string, error) {
	list, _, err := h.resolveTargetChannels(ctx, channels)
	if err != nil {
		return "", fmt.Errorf("auto-discover channels: %w", err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no channels available — pass channels, set SLACK_CHANNELS, or join some channels")
	}
	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)

	var decisions []string
	var digests []string
	for _, ch := range list {
		channelID, err := h.Channels().ResolveID(ctx, ch)
		if err != nil {
			digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
			continue
		}
		msgs, err := h.Messages().History(ctx, slack.HistoryParams{
			ChannelID: channelID,
			OldestTS:  float64(oldest.Unix()),
			Limit:     h.cfg.MaxMessagesPerChannel,
		})
		if err != nil {
			digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
			continue
		}
		users := h.resolveRefs(ctx, msgs)
		digests = append(digests, format.ChannelDigest("#"+ch, msgs, users, maxShow))
		decisions = append(decisions, detectDecisions(h.cfg, ch, msgs, users, format.DecisionLine)...)
	}

	var b strings.Builder
	if len(decisions) > 0 {
		b.WriteString("## Decisions\n")
		for _, line := range decisions {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("## Activity\n")
	b.WriteString(strings.Join(digests, "\n\n"))
	return b.String(), nil
}

func (h *Hub) channelDigest(ctx context.Context, channel string, hours, maxShow int, fullText bool) (string, error) {
	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)
	return h.channelDigestRange(ctx, channel, oldest, time.Time{}, maxShow, fullText, false, 0)
}

func (h *Hub) channelDigestRange(ctx context.Context, channel string, oldest, latest time.Time, maxShow int, fullText bool, withReplies bool, replyCap int) (string, error) {
	// resolveConversation (not bare ResolveID) so a DM works directly:
	// `@handle` and a bare `U…` user id — the shape this tool's own DM
	// headers print — both land on that person's DM without the caller
	// hunting for the D… id via search.
	channelID, err := h.resolveConversation(ctx, channel)
	if err != nil {
		return "", err
	}
	p := slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  float64(oldest.Unix()),
		Limit:     h.cfg.MaxMessagesPerChannel,
	}
	if !latest.IsZero() {
		p.LatestTS = float64(latest.Unix())
	}
	msgs, err := h.Messages().History(ctx, p)
	if err != nil {
		return "", err
	}

	// Thread drill-in: history returns only top-level messages, so a
	// channel whose real content lives in threads (a huddle with its
	// discussion, a request answered in replies) renders as bare
	// "(N replies)" counters. with_replies fetches those threads and
	// inlines them — closing the "digest said 2 replies but couldn't
	// show them" gap.
	var replies map[string][]goslack.Message
	if withReplies {
		replies = collectThreadReplies(ctx, h.Messages(), channelID, msgs)
	}

	nameSrc := msgs
	if len(replies) > 0 {
		nameSrc = append(append([]goslack.Message{}, msgs...), flattenReplies(replies)...)
	}
	users := h.resolveRefs(ctx, nameSrc)

	var opts []format.DigestOption
	if fullText {
		opts = append(opts, format.WithFullText())
	}
	if len(replies) > 0 {
		opts = append(opts, format.WithThreadReplies(replies), format.WithThreadPreviewReplies(replyCap))
	}
	return format.ChannelDigest("#"+channel, msgs, users, maxShow, opts...), nil
}

// isDMRef reports whether a conversation reference points at a direct
// message, decided from the reference SHAPE alone — no API call:
// `@handle` and a bare `U…`/`W…` user id always open a DM, a `D…` id is
// one, and a channel name or `C…/G…` id never is. Pure.
func isDMRef(ref string) bool {
	switch kind, token := classifyConversationRef(ref); {
	case kind == refHandle, kind == refUserID:
		return true
	case slack.IsChannelID(token):
		return false
	case strings.HasPrefix(strings.TrimPrefix(token, "#"), "D"):
		return slack.IsConversationID(strings.TrimPrefix(token, "#"))
	}
	// Plain channel name — a name never denotes a DM.
	return false
}

// collectThreadReplies fetches the replies for every thread parent in
// the window (thread_ts == ts, reply_count > 0), keyed by parent ts with
// the parent itself stripped. Best-effort: a single unreadable thread is
// skipped rather than failing the digest. Takes the narrow MessageClient
// so tests drive it with a fake.
func collectThreadReplies(ctx context.Context, msgs MessageClient, channelID string, window []goslack.Message) map[string][]goslack.Message {
	var out map[string][]goslack.Message
	for _, m := range window {
		if m.ThreadTimestamp == "" || m.ThreadTimestamp != m.Timestamp || m.ReplyCount == 0 {
			continue
		}
		thread, err := msgs.ThreadReplies(ctx, channelID, m.Timestamp)
		if err != nil {
			continue
		}
		var reps []goslack.Message
		for _, r := range thread {
			if r.Timestamp == m.Timestamp {
				continue // conversations.replies includes the parent
			}
			reps = append(reps, r)
		}
		if len(reps) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string][]goslack.Message)
		}
		out[m.Timestamp] = reps
	}
	return out
}

// flattenReplies concatenates all reply slices — used only to feed the
// user-name resolver, order irrelevant.
func flattenReplies(replies map[string][]goslack.Message) []goslack.Message {
	var out []goslack.Message
	for _, rs := range replies {
		out = append(out, rs...)
	}
	return out
}

// parseRange resolves the user's window into (oldest, latest). When
// after/before are set, they override hours. before is exclusive at
// end-of-day so "after=2026-04-30 before=2026-05-01" returns one full
// UTC day.
func parseRange(after, before string, hours int) (time.Time, time.Time, error) {
	if after == "" && before == "" {
		return time.Now().Add(-time.Duration(hours) * time.Hour), time.Time{}, nil
	}
	var oldest, latest time.Time
	if after != "" {
		t, err := time.Parse("2006-01-02", after)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("after must be YYYY-MM-DD: %w", err)
		}
		oldest = t
	}
	if before != "" {
		t, err := time.Parse("2006-01-02", before)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("before must be YYYY-MM-DD: %w", err)
		}
		latest = t.Add(24 * time.Hour)
	}
	if !oldest.IsZero() && !latest.IsZero() && !latest.After(oldest) {
		return time.Time{}, time.Time{}, fmt.Errorf("before must be after after")
	}
	return oldest, latest, nil
}

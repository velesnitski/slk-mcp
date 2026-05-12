package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/digest"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func (h *Hub) registerUnreadTools(s *server.MCPServer) {
	if !h.client.HasUserToken() {
		h.log.Info("user token not set; unread/mentions tools disabled",
			"hint", "set SLACK_USER_TOKEN=xoxp-... to enable")
		return
	}

	if !h.cfg.IsDisabled("get_unread_summary") {
		s.AddTool(
			mcp.NewTool("get_unread_summary",
				mcp.WithDescription("Smart summary of all unread messages across joined channels. Requires SLACK_USER_TOKEN."),
				mcp.WithNumber("max_per_channel", mcp.Description("Max unread messages to inline per channel (default: 20)")),
				mcp.WithBoolean("mentions_only", mcp.Description("If true, return only channels that contain a direct mention of the authenticated user (default: false)")),
				mcp.WithNumber("thread_preview_replies", mcp.Description("Max thread replies inlined per parent (default: 3)")),
				mcp.WithNumber("urgency_weight", mcp.Description("Multiplier on the urgency score before ranking (default: 1.0). Pass 0 or negative to use the default; pass 0.5 to dampen, 2.0 to amplify.")),
				mcp.WithString("urgency_keywords", mcp.Description("Comma-separated extra urgency keywords (case-insensitive substrings). Additive to the built-in en/ru list — e.g. 'asap, critical, p0, prod down'.")),
				mcp.WithString("log_mode", mcp.Description("Log-channel rendering: 'auto' (default — detect bot-driven channels and render them as severity histograms) or 'off' (always use the regular per-message digest).")),
				mcp.WithNumber("log_samples_per_band", mcp.Description("Max sample messages shown per severity band in log mode (default: 3)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				maxPer := int(req.GetFloat("max_per_channel", 20))
				mentionsOnly := req.GetBool("mentions_only", false)
				replyCap := int(req.GetFloat("thread_preview_replies", float64(format.ThreadPreviewReplies)))
				logMode := strings.ToLower(strings.TrimSpace(req.GetString("log_mode", "auto")))
				logSamples := int(req.GetFloat("log_samples_per_band", 3))
				urg := digest.UrgencyOpts{
					Weight:        req.GetFloat("urgency_weight", 0),
					ExtraKeywords: digest.ParseExtraKeywords(req.GetString("urgency_keywords", "")),
				}

				results, err := h.client.Unread.UnreadAll(ctx, maxPer)
				if err != nil {
					if errors.Is(err, slack.ErrNoUserToken) {
						return mcp.NewToolResultError(err.Error()), nil
					}
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Best-effort self-resolution for mention markers; a
				// failure here disables highlighting AND mentions_only
				// filtering (we can't filter what we can't identify).
				selfID, err := h.client.Unread.Self(ctx)
				if err != nil {
					h.log.Warn("auth.test failed; mention highlighting disabled", "err", err)
				}

				if mentionsOnly {
					if selfID == "" {
						return mcp.NewToolResultError("mentions_only requires auth.test to succeed; got an empty self id"), nil
					}
					results = filterMentions(results, selfID)
				}

				if len(results) == 0 {
					if mentionsOnly {
						return mcp.NewToolResultText("no unread channels mention you"), nil
					}
					return mcp.NewToolResultText("all caught up — 0 unread"), nil
				}

				now := time.Now()
				sort.Slice(results, func(i, j int) bool {
					return digest.RankUnread(results[i], selfID, now, urg) > digest.RankUnread(results[j], selfID, now, urg)
				})

				totalMsgs, totalReplies := 0, 0
				for _, r := range results {
					totalMsgs += len(r.Messages)
					for _, rs := range r.Replies {
						totalReplies += len(rs)
					}
				}

				var b strings.Builder
				header := "# Unread summary"
				if mentionsOnly {
					header = "# Unread summary (mentions only)"
				}
				fmt.Fprintf(&b, "%s\n%d channels, %d top-level + %d thread replies\n\n",
					header, len(results), totalMsgs, totalReplies)

				logChannels := 0
				for _, r := range results {
					users := h.resolveRefsWithReplies(ctx, r)
					label := channelDisplayLabel(ctx, r.Channel, h.client.Users)
					var rendered string
					switch {
					case logMode != "off" && digest.DetectGitChannel(r):
						logChannels++
						workflows, orphans := digest.GroupGitWorkflows(r.Messages)
						if len(workflows) == 0 && len(orphans) == 0 {
							continue
						}
						rendered = digest.RenderGitChannel(label, len(r.Messages), workflows, orphans)
					case logMode != "off" && digest.DetectLogChannel(r):
						logChannels++
						bands := digest.BuildLogBands(r.Messages, logSamples)
						rendered = format.LogChannelDigest(label, len(r.Messages), bands, users)
					case digest.DetectLowSignalChannel(r):
						rendered = digest.RenderLowSignalChannel(label, r)
					default:
						rendered = format.ChannelDigest(
							label, r.Messages, users, maxPer,
							format.WithMentionHighlight(selfID),
							format.WithThreadReplies(r.Replies),
							format.WithThreadPreviewReplies(replyCap),
						)
					}
					if rendered == "" {
						continue
					}
					b.WriteString(rendered)
					b.WriteString("\n\n")
				}
				if logChannels > 0 {
					h.log.Debug("log mode applied", "channels", logChannels)
				}
				if footer := digest.RenderReferences(digest.CollectReferences(results)); footer != "" {
					b.WriteString(footer)
					b.WriteString("\n")
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_mentions") {
		s.AddTool(
			mcp.NewTool("get_mentions",
				mcp.WithDescription("Messages that mention the authenticated user. Requires SLACK_USER_TOKEN."),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: 72)")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
				mcp.WithBoolean("with_context", mcp.Description("For each hit, fetch a few preceding messages from the same channel/DM (default: false)")),
				mcp.WithNumber("context_messages", mcp.Description("How many preceding messages to inline when with_context=true (default: 3)")),
				mcp.WithBoolean("pending_only", mcp.Description("Only keep mentions where you haven't posted a text reply afterwards (emoji reactions and file uploads don't count). Costs one conversations.history call per hit.")),
				mcp.WithBoolean("strict_mention", mcp.Description("Only keep matches where the operator's user id literally appears as <@SELFID> in the message body. Filters Slack-search false positives in shared channels (default: false)")),
				mcp.WithBoolean("drop_closing_acks", mcp.Description("Drop mentions whose body is a short closing acknowledgement (thanks/спасибо/ok/+1). Useful with pending_only (default: false)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				hours := int(req.GetFloat("hours", 72))
				limit := int(req.GetFloat("limit", 30))
				withContext := req.GetBool("with_context", false)
				ctxN := int(req.GetFloat("context_messages", 3))
				pendingOnly := req.GetBool("pending_only", false)
				strictMention := req.GetBool("strict_mention", false)
				dropAcks := req.GetBool("drop_closing_acks", false)

				after := time.Now().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02")
				q := fmt.Sprintf("to:me after:%s", after)

				matches, err := h.client.Search.Messages(ctx, q, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				selfID, _ := h.client.Unread.Self(ctx)

				if pendingOnly {
					if selfID == "" {
						return mcp.NewToolResultError("pending_only requires auth.test to succeed; got an empty self id"), nil
					}
					matches = filterEmptyMentions(matches)
					if dropAcks {
						matches = filterClosingAcks(matches)
					}
					matches = h.filterPendingMentions(ctx, matches, selfID)
				}
				if strictMention {
					if selfID == "" {
						return mcp.NewToolResultError("strict_mention requires auth.test to succeed; got an empty self id"), nil
					}
					matches = filterStrictMentions(matches, selfID)
				}

				if len(matches) == 0 {
					if pendingOnly {
						return mcp.NewToolResultText(fmt.Sprintf("no pending mentions in last %dh — every direct ask got a text reply from you", hours)), nil
					}
					return mcp.NewToolResultText(fmt.Sprintf("no mentions in last %dh", hours)), nil
				}

				var b strings.Builder
				header := fmt.Sprintf("%d mentions (last %dh)", len(matches), hours)
				if pendingOnly {
					header += " — pending (no text reply from you)"
				}
				b.WriteString(header)
				b.WriteByte('\n')
				shownContext := map[string]struct{}{}
				for _, m := range matches {
					b.WriteString(format.SearchResult(m))
					b.WriteByte('\n')
					if withContext && m.Channel.ID != "" {
						before, after := h.fetchMentionContext(ctx, m.Channel.ID, m.Timestamp, ctxN)
						users := h.client.Users.NamesFor(ctx, append(collectUserIDs(before), collectUserIDs(after)...))
						writeContextLines(&b, "    ↳ ", before, users, m.Channel.ID, shownContext)
						writeContextLines(&b, "    ↪ ", after, users, m.Channel.ID, shownContext)
					}
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !h.cfg.ReadOnly && !h.cfg.IsDisabled("mark_read") {
		s.AddTool(
			mcp.NewTool("mark_read",
				mcp.WithDescription("Mark a channel as read up to a given message timestamp. Pass either (channel + timestamp) or a Slack permalink."),
				mcp.WithString("channel", mcp.Description("Channel name (optional if permalink is provided)")),
				mcp.WithString("timestamp", mcp.Description("Message ts to mark read through (optional if permalink is provided)")),
				mcp.WithString("permalink", mcp.Description("Slack permalink to the message to mark read through — fills channel and timestamp in one go")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel := req.GetString("channel", "")
				ts := req.GetString("timestamp", "")
				permalink := req.GetString("permalink", "")

				if permalink != "" {
					p, err := slack.ParseSlackPermalink(permalink)
					if err != nil {
						return mcp.NewToolResultError("permalink could not be parsed: " + err.Error()), nil
					}
					if p != nil {
						// mark_read advances the read cursor up to a specific
						// message — we want the message's own ts, not its thread
						// root, so use TS even when the permalink points to a
						// reply.
						if channel == "" {
							channel = p.ChannelID
						}
						if ts == "" {
							ts = p.TS
						}
					}
				}

				if channel == "" {
					return mcp.NewToolResultError("channel is required (or pass a permalink)"), nil
				}
				if ts == "" {
					return mcp.NewToolResultError("timestamp is required (or pass a permalink)"), nil
				}

				channelID, err := h.client.Channels.ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := h.client.Unread.MarkRead(ctx, channelID, ts); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("marked #%s read up to %s", channel, ts)), nil
			},
		)
	}
}

// filterEmptyMentions drops matches whose body has no real text. An
// empty mention can't be "pending" — there was nothing to reply to.
func filterEmptyMentions(matches []goslack.SearchMessage) []goslack.SearchMessage {
	out := matches[:0]
	for _, m := range matches {
		if strings.TrimSpace(m.Text) != "" {
			out = append(out, m)
		}
	}
	return out
}

// closingAckRe matches short conversation-closing acknowledgements
// in en + ru. Anchored to whole-trimmed-body so partial matches in
// longer messages are not affected.
var closingAckRe = regexp.MustCompile(`(?i)^(?:thanks|thank you|thx|ok|okay|got it|spasibo|spasiba|спасибо|спасиб|пасиб|ок|окей|\+1|👍|:thumbsup:|:\+1:|np|nice|great|ack|done)[!.)\s]*$`)

func filterClosingAcks(matches []goslack.SearchMessage) []goslack.SearchMessage {
	out := matches[:0]
	for _, m := range matches {
		if !closingAckRe.MatchString(strings.TrimSpace(m.Text)) {
			out = append(out, m)
		}
	}
	return out
}

// filterStrictMentions removes matches that don't literally tag the
// operator via <@SELFID> in the body. Slack's `to:me` search
// occasionally surfaces channel-wide messages where you're a member
// but were never directly mentioned; this filter rejects those.
func filterStrictMentions(matches []goslack.SearchMessage, selfID string) []goslack.SearchMessage {
	needle := "<@" + selfID + ">"
	prefixed := "<@" + selfID + "|"
	out := matches[:0]
	for _, m := range matches {
		if strings.Contains(m.Text, needle) || strings.Contains(m.Text, prefixed) {
			out = append(out, m)
		}
	}
	return out
}

// writeContextLines renders prior/subsequent context messages with
// two filters: (1) skip messages already shown for this channel
// (dedup across consecutive same-channel mentions), and (2) skip
// messages with no signal (HasContent == false) so empty Slackbot
// pings and reaction-only entries don't waste tokens.
func writeContextLines(b *strings.Builder, prefix string, msgs []goslack.Message, users map[string]string, channelID string, shown map[string]struct{}) {
	for _, p := range msgs {
		key := channelID + "|" + p.Timestamp
		if _, ok := shown[key]; ok {
			continue
		}
		shown[key] = struct{}{}
		if !format.HasContent(p) {
			continue
		}
		b.WriteString(prefix)
		b.WriteString(format.MessageLine(p, users[p.User], users))
		b.WriteByte('\n')
	}
}

// filterPendingMentions keeps only mentions where the operator
// (selfID) hasn't posted a text reply in the same channel after the
// mention timestamp. Reactions and empty messages do NOT count as
// replies. One conversations.history call per match (4-worker pool).
func (h *Hub) filterPendingMentions(ctx context.Context, matches []goslack.SearchMessage, selfID string) []goslack.SearchMessage {
	const workers = 4
	type job struct {
		idx   int
		match goslack.SearchMessage
	}
	type result struct {
		idx     int
		pending bool
	}

	jobs := make(chan job, len(matches))
	results := make(chan result, len(matches))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- result{idx: j.idx, pending: !h.operatorReplied(ctx, j.match.Channel.ID, j.match.Timestamp, selfID)}
			}
		}()
	}
	for i, m := range matches {
		jobs <- job{idx: i, match: m}
	}
	close(jobs)
	wg.Wait()
	close(results)

	keep := make([]bool, len(matches))
	for r := range results {
		keep[r.idx] = r.pending
	}
	out := matches[:0]
	for i, m := range matches {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

func (h *Hub) operatorReplied(ctx context.Context, channelID, mentionTS, selfID string) bool {
	pivot, _ := strconv.ParseFloat(mentionTS, 64)
	if pivot <= 0 || channelID == "" {
		return false
	}
	hist, err := h.client.Messages.History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  pivot,
		Limit:     20,
	})
	if err != nil {
		h.log.Debug("operator-reply check failed", "channel", channelID, "err", err)
		return false
	}
	for _, m := range hist {
		if m.Timestamp <= mentionTS {
			continue
		}
		if m.User != selfID {
			continue
		}
		if collapseTextEmpty(m.Text) {
			continue
		}
		return true
	}
	return false
}

func collapseTextEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// fetchMentionContext returns up to n messages on each side of
// `pivotTS` in `channelID`, ordered oldest → newest. Both lists are
// best-effort and may be empty on error.
func (h *Hub) fetchMentionContext(ctx context.Context, channelID, pivotTS string, n int) (before, after []goslack.Message) {
	if n <= 0 {
		n = 3
	}
	hist, err := h.client.Messages.History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		Limit:     n + 1,
	})
	if err != nil {
		h.log.Debug("fetch mention context (before) failed", "channel", channelID, "err", err)
	} else {
		for _, m := range hist {
			if m.Timestamp >= pivotTS {
				continue
			}
			before = append(before, m)
			if len(before) >= n {
				break
			}
		}
	}

	pivot, _ := strconv.ParseFloat(pivotTS, 64)
	if pivot > 0 {
		hist, err := h.client.Messages.History(ctx, slack.HistoryParams{
			ChannelID: channelID,
			OldestTS:  pivot,
			Limit:     n + 1,
		})
		if err != nil {
			h.log.Debug("fetch mention context (after) failed", "channel", channelID, "err", err)
		} else {
			for _, m := range hist {
				if m.Timestamp <= pivotTS {
					continue
				}
				after = append(after, m)
				if len(after) >= n {
					break
				}
			}
		}
	}

	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	for i, j := 0, len(after)-1; i < j; i, j = i+1, j-1 {
		after[i], after[j] = after[j], after[i]
	}
	return before, after
}

// channelDisplayLabel returns the human-friendly heading used in
// digest output:
//
//   - "#name" for public/private channels.
//   - "@peer" for direct messages (1:1) — peer ID resolved via
//     UserService for a friendly handle.
//   - the raw channel name (typically "mpdm-a--b--c-1") for
//     multi-party DMs, since Slack's mpim names already convey
//     who's in the conversation.
//
// Falls back to "#?" when nothing usable is available.
func channelDisplayLabel(ctx context.Context, ch goslack.Channel, users *slack.UserService) string {
	switch {
	case ch.IsIM:
		if ch.User != "" {
			return "@" + users.Name(ctx, ch.User)
		}
		return "@?"
	case ch.IsMpIM:
		if ch.Name != "" {
			return ch.Name
		}
		return "mpdm-?"
	default:
		if ch.Name != "" {
			return "#" + ch.Name
		}
		return "#?"
	}
}

// rankUnread / channelMentions moved to internal/digest (rank.go).

// filterMentions returns only ChannelUnread entries that contain at
// least one direct mention of selfID, in either a top-level message
// or a thread reply. selfID must be non-empty; pass through unchanged
// if filtering should be a no-op.
func filterMentions(results []*slack.ChannelUnread, selfID string) []*slack.ChannelUnread {
	if selfID == "" {
		return results
	}
	out := results[:0]
	for _, r := range results {
		if digest.ChannelMentions(r, selfID) {
			out = append(out, r)
		}
	}
	return out
}

// resolveRefsWithReplies is the unread-channel counterpart of
// resolveRefs: gathers user IDs and channel IDs across top-level
// messages AND inlined thread replies, then merges the resolved
// names. Used by the unread-summary renderer so `<@UID>` and
// `<#CID>` references inside thread bodies render readably.
func (h *Hub) resolveRefsWithReplies(ctx context.Context, cu *slack.ChannelUnread) map[string]string {
	users := h.client.Users.NamesFor(ctx, collectUserIDsWithReplies(cu))
	channels := h.client.Channels.NamesForIDs(ctx, collectChannelIDsWithReplies(cu))
	return mergeRefs(users, channels)
}

// collectChannelIDsWithReplies mirrors collectUserIDsWithReplies for
// `<#CHANNELID>` references — scans both the top-level unread
// messages and their inlined thread replies, deduping by ID.
func collectChannelIDsWithReplies(cu *slack.ChannelUnread) []string {
	seen := make(map[string]struct{})
	var ids []string
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, id := range format.CollectMentionedChannelIDs(cu.Messages) {
		add(id)
	}
	for _, rs := range cu.Replies {
		for _, id := range format.CollectMentionedChannelIDs(rs) {
			add(id)
		}
	}
	return ids
}

// collectUserIDsWithReplies returns unique user IDs across both the
// channel's top-level unread messages and any inlined thread replies,
// PLUS any users referenced via <@USERID> mentions inside message
// bodies. Pre-resolving mentioned users makes RenderText able to
// substitute readable names instead of raw IDs.
func collectUserIDsWithReplies(cu *slack.ChannelUnread) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(cu.Messages))
	add := func(uid string) {
		if uid == "" {
			return
		}
		if _, ok := seen[uid]; ok {
			return
		}
		seen[uid] = struct{}{}
		ids = append(ids, uid)
	}
	for _, m := range cu.Messages {
		add(m.User)
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			add(r.User)
		}
	}
	for _, id := range format.CollectMentionedUserIDs(cu.Messages) {
		add(id)
	}
	for _, rs := range cu.Replies {
		for _, id := range format.CollectMentionedUserIDs(rs) {
			add(id)
		}
	}
	return ids
}

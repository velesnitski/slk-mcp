package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/digest"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// unreadParams bundles the parsed get_unread_summary arguments so the
// per-workspace body (buildUnreadSummary) can be invoked once per
// workspace from the registry loop without re-reading the request.
type unreadParams struct {
	maxPer             int
	mentionsOnly       bool
	replyCap           int
	logMode            string
	logSamples         int
	skipLog            bool
	skipGit            bool
	maxChars           int
	dmWindowHours      int
	priorityHours      int
	threadMentionHours int
	ownThreadHours     int
	canvasHours        int
	afterTS            string
	includeRefs        bool
	showAnswered       bool
	dmFullText         bool
	urg                digest.UrgencyOpts
}

// mentionParams bundles the parsed get_mentions arguments for the same
// per-workspace reuse as unreadParams.
type mentionParams struct {
	hours         int
	limit         int
	withContext   bool
	ctxN          int
	pendingOnly   bool
	strictMention bool
	dropAcks      bool
	dmHistory     bool
	summaryOnly   bool
}

const (
	// maxCharsAuto is the sentinel for "caller didn't set max_chars" — it
	// triggers the auto budget in runUnreadSummary. Distinct from an
	// explicit 0 (unlimited).
	maxCharsAuto = -1

	// dmMessageLimit is the per-message body cap for DM channels in the
	// unread sweep (vs the 280-char MessageLineLimit used for channels).
	// Generous enough for amounts/deadlines/short paragraphs, bounded so
	// a pasted wall-of-text can't dominate the char budget.
	dmMessageLimit = 1500

	// DefaultTotalMaxChars bounds the combined rendered body across all
	// workspaces when max_chars is left to auto. Chosen to stay well under
	// the MCP host's result-size limit (a weekend backlog rendered ~57k
	// chars and overflowed); the budget is split evenly across the
	// workspaces being rendered. Channels beyond the cap collapse to the
	// "+N channels omitted" footer rather than overflowing.
	DefaultTotalMaxChars = 24000
)

// resolveMaxChars turns the max_chars sentinel into a concrete per-workspace
// budget. <0 (auto, the default) → DefaultTotalMaxChars split across the n
// workspaces being rendered; 0 (unlimited) and an explicit positive cap pass
// through unchanged.
func resolveMaxChars(maxChars, n int) int {
	if maxChars >= 0 {
		return maxChars
	}
	if n < 1 {
		n = 1
	}
	return DefaultTotalMaxChars / n
}

func (h *Hub) registerUnreadTools(s *server.MCPServer) {
	// Register when ANY workspace carries a user token — a secondary
	// workspace's xoxp- is reason enough to expose the tools even if the
	// primary is bot-only. buildUnreadSummary skips token-less workspaces
	// per-section at run time.
	anyUserToken := false
	for _, ws := range h.registry {
		if ws.Client.HasUserToken() {
			anyUserToken = true
			break
		}
	}
	if !anyUserToken {
		h.log.Info("user token not set on any workspace; unread/mentions tools disabled",
			"hint", "set SLACK_USER_TOKEN=xoxp-... to enable")
		return
	}

	if !h.cfg.IsDisabled("get_unread_summary") {
		s.AddTool(
			mcp.NewTool("get_unread_summary",
				mcp.WithDescription("Smart summary of all unread messages across joined channels. Requires SLACK_USER_TOKEN. With multiple workspaces configured, merges every workspace under a [label] heading. Fetching is time-bounded (SLACK_SWEEP_TIMEOUT): an 'incomplete:' line names what was not read, and that workspace's cursor is not advanced, so passing the returned cursor re-reads it."),
				mcp.WithNumber("max_per_channel", mcp.Description("Max unread messages to inline per channel (default: 20)")),
				mcp.WithBoolean("mentions_only", mcp.Description("If true, return only channels that contain a direct mention of the authenticated user (default: false)")),
				mcp.WithNumber("thread_preview_replies", mcp.Description("Max thread replies inlined per parent (default: 3)")),
				mcp.WithNumber("urgency_weight", mcp.Description("Multiplier on the urgency score before ranking (default: 1.0). Pass 0 or negative to use the default; pass 0.5 to dampen, 2.0 to amplify.")),
				mcp.WithString("urgency_keywords", mcp.Description("Comma-separated extra urgency keywords (case-insensitive substrings). Additive to the built-in en/ru list — e.g. 'asap, critical, p0, prod down'.")),
				mcp.WithString("log_mode", mcp.Description("Log-channel rendering: 'auto' (default — detect bot-driven channels and render them as severity histograms) or 'off' (always use the regular per-message digest).")),
				mcp.WithNumber("log_samples_per_band", mcp.Description("Max sample messages shown per severity band in log mode (default: 1; raise for more inline samples)")),
				mcp.WithBoolean("skip_log_mode", mcp.Description("If true, omit log-mode channels (alert/error feeds) entirely. Cheap way to shrink the output when bot channels dominate (default: false)")),
				mcp.WithBoolean("skip_git_mode", mcp.Description("If true, omit git-mode channels (CI / git-bot feeds) entirely. Cheap way to shrink the output when git activity dominates (default: false)")),
				mcp.WithNumber("max_chars", mcp.Description("Soft cap on rendered body size (in characters), per workspace. Channels are emitted in urgency order; once the cap is reached, remaining channels are listed in a footer instead of inlined. Omit (default) to auto-cap to a total budget split across workspaces so a large backlog can't overflow the result. Pass 0 for unlimited, or a positive N for a hard per-workspace cap.")),
				mcp.WithNumber("priority_hours", mcp.Description("Window for the configured priority channels (SLACK_PRIORITY_CHANNELS / priority_channels per workspace): they are shown for this many hours whether or not you have read them, first in the output and marked ★. With `after`, everything newer than the cursor is shown instead. Default: 24; 0 = off.")),
				mcp.WithNumber("dm_window_hours", mcp.Description("If > 0, also include DM and multi-party-DM conversations with activity in the last N hours, regardless of last_read. Surfaces threads the operator has already opened (decisions made in DMs, exec sync that has been read). 0 = disabled (default), DMs surface only when actually unread.")),
				mcp.WithNumber("thread_mention_hours", mcp.Description("If > 0, additionally surface channels where the operator was @-mentioned in a thread reply within the last N hours, even when the thread parent is already read. Closes a silent-miss gap in the unread sweep — Slack pings the operator, but UnreadAll's reply fetch only covers replies to NEW top-level messages. Default: 24 (recommended).")),
				mcp.WithNumber("own_thread_hours", mcp.Description("If > 0, additionally surface NEW replies in threads the operator STARTED or already replied in, even when nobody @-mentioned them (Slack auto-follows those threads but never marks the channel unread). Catches a colleague answering your own request. Default: 24 (recommended).")),
				mcp.WithNumber("canvas_hours", mcp.Description("If > 0, also report CANVASES updated in the last N hours, flagging the ones that @-mention the operator. A canvas edit produces no channel message at all — Slack notifies the person tagged inside the document, but conversations.history has nothing to return and search does not index canvas bodies — so without this a tag in a canvas is invisible to the sweep. When `after` is passed, the cursor wins and this only acts as the on/off switch. Default: 24 (recommended); 0 = off.")),
				mcp.WithString("after", mcp.Description("Delta cursor. Preferred: the combined token from the previous pull's trailing 'cursor:' line (e.g. 'primary=1784012484.4;secondary=1784011290.7') — applies each workspace's own cursor exactly. A plain Slack timestamp also works and applies to every workspace. Only messages/replies strictly newer are returned; channels with nothing new are dropped. Empty (default) = full sweep.")),
				mcp.WithBoolean("include_refs", mcp.Description("Append the trailing References block (every issue ID, MR, and branch seen). Off by default — it costs a few hundred tokens and is only worth it when you'll chain into those IDs. The newest message ts also lives in each channel's inline output, so a delta cursor doesn't need this.")),
				mcp.WithBoolean("show_answered", mcp.Description("Also show DMs where the operator already holds the last word. By default those are suppressed to a one-line note — Slack's last_read updates on client focus, not on send, so a DM answered from a notification can stay 'unread' server-side and re-surface as a false pending item. Default: false.")),
				mcp.WithBoolean("dm_full_text", mcp.Description("Render DM message bodies at a generous cap (1500 chars) instead of the compact 280-char channel preview, so amounts/deadlines/asks in a direct message aren't truncated to '(+N chars)'. DMs only — channels stay compact. Default: true; set false for a leaner, uniformly-truncated sweep.")),
				mcp.WithString("workspace", mcp.Description("Limit to a single workspace by its configured label. Default: merge every configured workspace.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				p := unreadParams{
					maxPer:             int(req.GetFloat("max_per_channel", 20)),
					mentionsOnly:       req.GetBool("mentions_only", false),
					replyCap:           int(req.GetFloat("thread_preview_replies", float64(format.ThreadPreviewReplies))),
					logMode:            strings.ToLower(strings.TrimSpace(req.GetString("log_mode", "auto"))),
					logSamples:         int(req.GetFloat("log_samples_per_band", 1)),
					skipLog:            req.GetBool("skip_log_mode", false),
					skipGit:            req.GetBool("skip_git_mode", false),
					maxChars:           int(req.GetFloat("max_chars", maxCharsAuto)),
					dmWindowHours:      int(req.GetFloat("dm_window_hours", 0)),
					priorityHours:      int(req.GetFloat("priority_hours", 24)),
					threadMentionHours: int(req.GetFloat("thread_mention_hours", 24)),
					ownThreadHours:     int(req.GetFloat("own_thread_hours", 24)),
					canvasHours:        int(req.GetFloat("canvas_hours", 24)),
					afterTS:            strings.TrimSpace(req.GetString("after", "")),
					includeRefs:        req.GetBool("include_refs", false),
					showAnswered:       req.GetBool("show_answered", false),
					dmFullText:         req.GetBool("dm_full_text", true),
					urg: digest.UrgencyOpts{
						Weight:        req.GetFloat("urgency_weight", 0),
						ExtraKeywords: digest.ParseExtraKeywords(req.GetString("urgency_keywords", "")),
					},
				}
				return h.runUnreadSummary(ctx, p, req.GetString("workspace", "")), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_mentions") {
		s.AddTool(
			mcp.NewTool("get_mentions",
				mcp.WithDescription("Messages that mention the authenticated user. Requires SLACK_USER_TOKEN. With multiple workspaces configured, merges every workspace under a [label] heading. Fetching is time-bounded (SLACK_SWEEP_TIMEOUT): an 'incomplete:' line means the list is partial, not that there are no further mentions."),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: 72)")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
				mcp.WithBoolean("with_context", mcp.Description("For each hit, fetch a few preceding messages from the same channel/DM (default: false)")),
				mcp.WithNumber("context_messages", mcp.Description("How many preceding messages to inline when with_context=true (default: 3)")),
				mcp.WithBoolean("pending_only", mcp.Description("Only keep mentions where you haven't posted a text reply afterwards (emoji reactions and file uploads don't count). Costs one conversations.history call per hit.")),
				mcp.WithBoolean("strict_mention", mcp.Description("Only keep matches where the operator's user id literally appears as <@SELFID> in the message body. Filters Slack-search false positives in shared channels (default: false)")),
				mcp.WithBoolean("drop_closing_acks", mcp.Description("Drop mentions whose body is a short closing acknowledgement (thanks/спасибо/ok/+1). Useful with pending_only (default: false)")),
				mcp.WithBoolean("dm_history", mcp.Description("Backstop DMs against Slack search's indexing lag: also read recent DM history directly so a message the other party just sent isn't silently missed (Slack's `to:me` search can lag DMs by minutes). Default: true. Set false to skip the extra history reads for a faster, search-only sweep.")),
				mcp.WithBoolean("summary", mcp.Description("Return aggregate stats instead of the hit list: total, DM vs channel split, per-sender and per-channel counts. Built for operational-load reporting ('how often was I pinged, by whom, where'). Composes with pending_only (then it summarises only unanswered mentions). Default: false.")),
				mcp.WithString("workspace", mcp.Description("Limit to a single workspace by its configured label. Default: merge every configured workspace.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				p := mentionParams{
					hours:         int(req.GetFloat("hours", 72)),
					limit:         int(req.GetFloat("limit", 30)),
					withContext:   req.GetBool("with_context", false),
					ctxN:          int(req.GetFloat("context_messages", 3)),
					pendingOnly:   req.GetBool("pending_only", false),
					strictMention: req.GetBool("strict_mention", false),
					dropAcks:      req.GetBool("drop_closing_acks", false),
					dmHistory:     req.GetBool("dm_history", true),
					summaryOnly:   req.GetBool("summary", false),
				}
				return h.runMentions(ctx, p, req.GetString("workspace", "")), nil
			},
		)
	}

	if !h.cfg.ReadOnly && !h.cfg.IsDisabled("mark_read") {
		s.AddTool(
			mcp.NewTool("mark_read",
				mcp.WithDescription("Mark a channel as read up to a given message timestamp. Pass either (channel + timestamp) or a Slack permalink. A permalink routes to the workspace it came from."),
				mcp.WithString("channel", mcp.Description("Channel name (optional if permalink is provided)")),
				mcp.WithString("timestamp", mcp.Description("Message ts to mark read through (optional if permalink is provided)")),
				mcp.WithString("permalink", mcp.Description("Slack permalink to the message to mark read through — fills channel and timestamp in one go")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel := req.GetString("channel", "")
				ts := req.GetString("timestamp", "")
				permalink := req.GetString("permalink", "")

				// mark_read advances the read cursor up to a specific
				// message — we want the message's own ts, not its thread
				// root, so useThreadTS is false even for reply permalinks.
				var errRes *mcp.CallToolResult
				channel, ts, errRes = resolveMessageRef(permalink, channel, ts, false)
				if errRes != nil {
					return errRes, nil
				}

				// Route by the permalink's host (or the explicit
				// workspace). This tool used to be primary-only, so a
				// permalink from the second workspace resolved a channel
				// the primary does not have. ADR 110.
				scoped, wsName, note, errRes := h.routeWorkspace(ctx, req.GetString("workspace", ""), permalink)
				if errRes != nil {
					return errRes, nil
				}
				channelID, err := scoped.Channels().ResolveID(ctx, channel)
				if err != nil {
					return withRouteNote(mcp.NewToolResultError(err.Error()), note), nil
				}
				if err := scoped.Unread().MarkRead(ctx, channelID, ts); err != nil {
					return withRouteNote(mcp.NewToolResultError(err.Error()), note), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("marked %s read up to %s%s", conversationLabel(channel), ts, h.wsLabel(wsName))), nil
			},
		)
	}
}

// runUnreadSummary fans the unread sweep across the requested workspaces
// and composes the result. One workspace (the common case, or an explicit
// `workspace` argument) renders exactly as the single-workspace tool
// always has. Two or more render each under a `## [label]` heading.
func (h *Hub) runUnreadSummary(ctx context.Context, p unreadParams, workspace string) *mcp.CallToolResult {
	targets, excluded := h.sweepTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	// Auto-cap: when the caller didn't pass max_chars (sentinel < 0), bound
	// the total output so a large backlog can't blow the MCP result limit,
	// splitting the budget across the workspaces being rendered. Explicit 0
	// stays unlimited; an explicit positive N stays a hard per-workspace cap.
	p.maxChars = resolveMaxChars(p.maxChars, len(targets))
	titleSuffix := ""
	if p.mentionsOnly {
		titleSuffix = " (mentions only)"
	}
	// An excluded workspace forces the multi-workspace frame: its note and
	// its carried-over cursor have nowhere to go in the single-body form.
	multi := len(targets) > 1 || len(excluded) > 0

	// Per-workspace delta cursors: the combined "ws=ts;ws2=ts2" token (as
	// emitted below) applies each workspace's own cursor; a plain ts still
	// applies to every workspace (the historical form). This removes the
	// caller-side "take the min of two cursors" dance, which re-showed
	// already-seen messages in the faster workspace.
	perWSAfter, plainAfter := parseAfterCursor(p.afterTS)
	cursors := make(map[string]string, len(targets))

	// One deadline for the whole fetch, across workspaces (ADR 121).
	// Rendering runs on ctx: names resolve from caches and must not be
	// cut to raw ids because the fetch used the budget up.
	fetchCtx, cancel := sweepContext(ctx, h.cfg.SweepTimeoutSeconds)
	defer cancel()

	// Workspaces run concurrently (ADR 122); rendering below walks the
	// results in registry order, exactly as the sequential loop did.
	type wsUnread struct {
		noToken             bool
		body, cursor, after string
		err                 error
	}
	results := fanOut(targets, func(ws slack.Workspace) wsUnread {
		scoped := h.withClient(ws.Client)
		r := wsUnread{after: cursorForWorkspace(perWSAfter, plainAfter, ws.Name)}
		if !scoped.client.HasUserToken() {
			r.noToken = true
			return r
		}
		// A deadline that has already passed needs no special case: the
		// sweep's first phase is then cut, and it reports itself incomplete
		// with the incoming cursor (ADR 121).
		pws := p
		pws.afterTS = r.after
		sw := newSweep(fetchCtx, scoped.log, "get_unread_summary", ws.Name)
		t0 := time.Now()
		r.body, r.cursor, r.err = scoped.sweepUnread(ctx, sw, pws)
		scoped.log.Info("sweep workspace", "tool", "get_unread_summary", "workspace", ws.Name,
			"took", time.Since(t0).Round(time.Millisecond).String())
		return r
	})
	for _, ws := range excluded {
		// Not read, so not advanced: a later explicit pull starts where it left off.
		if c := cursorForWorkspace(perWSAfter, plainAfter, ws.Name); c != "" {
			cursors[ws.Name] = c
		}
	}

	var sections []string
	for i, ws := range targets {
		r := results[i]
		if r.noToken {
			if multi {
				sections = append(sections, workspaceSection(ws.Name, "_skipped: no user token for this workspace_"))
			}
			continue
		}
		body, wsCursor, err := r.body, r.cursor, r.err
		if wsCursor == "" {
			wsCursor = r.after // never regress a workspace's cursor
		}
		cursors[ws.Name] = wsCursor
		switch {
		case err != nil && !multi:
			return mcp.NewToolResultError(err.Error())
		case err != nil:
			sections = append(sections, workspaceSection(ws.Name, "_error: "+err.Error()+"_"))
		case body == "" && !multi:
			return mcp.NewToolResultText(unreadEmptyMsg(p.mentionsOnly))
		case body == "":
			sections = append(sections, workspaceSection(ws.Name, unreadEmptyMsg(p.mentionsOnly)))
		case !multi:
			return mcp.NewToolResultText("# Unread summary" + titleSuffix + " · " + format.TimeZoneNote() + "\n" + body)
		default:
			sections = append(sections, workspaceSection(ws.Name, body))
		}
	}

	header := fmt.Sprintf("# Unread summary%s — %d workspaces · %s", titleSuffix, len(targets), format.TimeZoneNote())
	out := header + "\n\n" + strings.Join(sections, "\n\n")
	if note := excludedNote(excluded); note != "" {
		out = strings.TrimRight(out, "\n") + "\n\n" + note
	}
	// One exact token for the next delta pull — no more taking the min of
	// the per-workspace cursor lines by hand. Excluded workspaces keep their
	// incoming cursor in it, in registry order.
	if combined := combinedCursor(workspaceOrder(h.workspaceTargets(workspace)), cursors); combined != "" {
		out = strings.TrimRight(out, "\n") + "\n\ncursor: " + combined + " (pass as after= next pull for an exact per-workspace delta)"
	}
	return mcp.NewToolResultText(strings.TrimRight(out, "\n"))
}

// workspaceOrder projects the registry-ordered target list to names,
// keeping the combined cursor deterministic (primary first).
func workspaceOrder(targets []slack.Workspace) []string {
	names := make([]string, 0, len(targets))
	for _, ws := range targets {
		names = append(names, ws.Name)
	}
	return names
}

// runMentions is the get_mentions analogue of runUnreadSummary.
func (h *Hub) runMentions(ctx context.Context, p mentionParams, workspace string) *mcp.CallToolResult {
	targets, excluded := h.sweepTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	multi := len(targets) > 1 || len(excluded) > 0

	fetchCtx, cancel := sweepContext(ctx, h.cfg.SweepTimeoutSeconds)
	defer cancel()

	type wsMentions struct {
		noToken bool
		body    string
		err     error
	}
	results := fanOut(targets, func(ws slack.Workspace) wsMentions {
		scoped := h.withClient(ws.Client)
		if !scoped.client.HasUserToken() {
			return wsMentions{noToken: true}
		}
		sw := newSweep(fetchCtx, scoped.log, "get_mentions", ws.Name)
		t0 := time.Now()
		body, err := scoped.sweepMentions(ctx, sw, p)
		scoped.log.Info("sweep workspace", "tool", "get_mentions", "workspace", ws.Name,
			"took", time.Since(t0).Round(time.Millisecond).String())
		return wsMentions{body: body, err: err}
	})

	var sections []string
	for i, ws := range targets {
		r := results[i]
		if r.noToken {
			if multi {
				sections = append(sections, workspaceSection(ws.Name, "_skipped: no user token for this workspace_"))
			}
			continue
		}
		body, err := r.body, r.err
		switch {
		case err != nil && !multi:
			return mcp.NewToolResultError(err.Error())
		case err != nil:
			sections = append(sections, workspaceSection(ws.Name, "_error: "+err.Error()+"_"))
		case body == "" && !multi:
			return mcp.NewToolResultText(mentionsEmptyMsg(p.pendingOnly, p.hours))
		case body == "":
			sections = append(sections, workspaceSection(ws.Name, mentionsEmptyMsg(p.pendingOnly, p.hours)))
		case !multi:
			return mcp.NewToolResultText(body)
		default:
			sections = append(sections, workspaceSection(ws.Name, body))
		}
	}

	header := fmt.Sprintf("# Mentions — %d workspaces", len(targets))
	out := header + "\n\n" + strings.Join(sections, "\n\n")
	if note := excludedNote(excluded); note != "" {
		out = strings.TrimRight(out, "\n") + "\n\n" + note
	}
	return mcp.NewToolResultText(strings.TrimRight(out, "\n"))
}

// buildUnreadSummary renders the unread sweep for the single workspace
// this Hub is scoped to (via withClient). It returns the body WITHOUT the
// top-level "# Unread summary" title (the caller owns the title so it can
// switch between single- and multi-workspace framing); an empty string
// means "nothing unread", which the caller turns into the caught-up
// message.
func (h *Hub) buildUnreadSummary(ctx context.Context, p unreadParams) (body, cursor string, err error) {
	return h.sweepUnread(ctx, newSweep(ctx, h.log, "get_unread_summary", ""), p)
}

// sweepUnread is buildUnreadSummary with its fetch phases run through sw
// (ADR 121): each is timed, and once sw's deadline passes the rest are
// skipped and named. Any cut phase keeps the incoming cursor — advancing
// it would make the next delta pull skip what this pull never read.
func (h *Hub) sweepUnread(ctx context.Context, sw *sweep, p unreadParams) (body, cursor string, err error) {
	var results []*slack.ChannelUnread
	if err := sw.run("unread", func(c context.Context) error {
		r, e := h.Unread().UnreadAll(c, p.maxPer)
		results = r
		return e
	}); err != nil {
		if sw.incomplete() {
			return strings.TrimRight(sw.note(true), "\n"), p.afterTS, nil
		}
		return "", "", err
	}

	// DM time-window override: when the operator wants to see DMs they've
	// already opened (executive syncs, side chats with decisions), pull
	// recent activity regardless of last_read and merge it on top of the
	// unread sweep. Same-channel DM entries replace their UnreadAll
	// counterparts so we don't duplicate.
	if p.dmWindowHours > 0 {
		var dmResults []*slack.ChannelUnread
		dmErr := sw.run("dm-window", func(c context.Context) error {
			r, e := h.Unread().RecentDMActivity(c, p.dmWindowHours, p.maxPer)
			dmResults = r
			return e
		})
		if dmErr != nil {
			h.log.Warn("dm window fetch failed; falling back to unread-only", "err", dmErr)
		} else {
			results = mergeDMOverride(results, dmResults)
		}
	}

	// Priority channels (ADR 112): shown for their window whether or not
	// they have been read. The sweep above is last_read-based, so a channel
	// the operator keeps up with in the client never appears in it — even
	// when that is where decisions are made. With a delta cursor the window
	// starts at the cursor, so a re-pull shows only what is new there.
	var priorityNote string
	if refs := h.client.Config().PriorityChannels; len(refs) > 0 && p.priorityHours > 0 {
		oldest := float64(time.Now().Add(-time.Duration(p.priorityHours) * time.Hour).Unix())
		if ts, perr := strconv.ParseFloat(p.afterTS, 64); perr == nil && ts > 0 {
			oldest = ts
		}
		var pr []*slack.ChannelUnread
		var missing []string
		prErr := sw.run("priority", func(c context.Context) error {
			r, m, e := h.Unread().PriorityActivity(c, refs, oldest, p.maxPer)
			pr, missing = r, m
			return e
		})
		if prErr != nil {
			h.log.Warn("priority channels fetch failed", "err", prErr)
			if sw.ctx.Err() == nil { // a deadline cut is reported once, by the sweep note
				priorityNote = "priority channels could not be read: " + prErr.Error() + "\n"
			}
		} else {
			results = mergePriority(results, pr)
		}
		if len(missing) > 0 {
			priorityNote += "priority channel(s) not found or not joined: " + strings.Join(missing, ", ") + "\n"
		}
	}

	// Thread-mention backstop: UnreadAll's fetchReplies only covers replies
	// to NEW top-level messages. Search-based `to:me` catches replies to
	// already-read parents; merge their channels into results.
	if p.threadMentionHours > 0 {
		var tmResults []*slack.ChannelUnread
		tmErr := sw.run("thread-mentions", func(c context.Context) error {
			r, e := h.Unread().UnreadThreadMentions(c, p.threadMentionHours)
			tmResults = r
			return e
		})
		if tmErr != nil {
			h.log.Warn("thread-mention backstop failed; falling back to unread-only", "err", tmErr)
		} else {
			results = mergeThreadMentions(results, tmResults)
		}
	}

	// Own-thread backstop: replies in a thread the operator started or
	// replied in that DON'T @-mention them are invisible to both the
	// unread sweep (parent already read) and the to:me mention pass.
	// `from:me` discovers those threads and surfaces replies newer than
	// the operator's last message. Reuses mergeThreadMentions (augment,
	// never replace).
	if p.ownThreadHours > 0 {
		var otResults []*slack.ChannelUnread
		otErr := sw.run("own-threads", func(c context.Context) error {
			r, e := h.Unread().UnreadOwnThreads(c, p.ownThreadHours)
			otResults = r
			return e
		})
		if otErr != nil {
			h.log.Warn("own-thread backstop failed; falling back to unread-only", "err", otErr)
		} else {
			results = mergeThreadMentions(results, otResults)
		}
	}

	// Best-effort self-resolution for mention markers; a failure here
	// disables highlighting AND mentions_only filtering (we can't filter
	// what we can't identify).
	selfID, err := h.Unread().Self(ctx)
	if err != nil {
		h.log.Warn("auth.test failed; mention highlighting disabled", "err", err)
	}

	// Canvas delta. Canvases sit outside the message model entirely — an
	// edit produces no channel message — so this is computed independently
	// of `results` and must survive an otherwise-empty sweep: a canvas
	// edit can be the only thing that happened since the cursor.
	var canvasBlock string
	if p.canvasHours > 0 {
		nowUnix := time.Now().Unix()
		var hits []canvasHit
		// canvasDelta degrades to "no canvases" on its own errors, so only
		// the deadline can mark this phase cut.
		_ = sw.run("canvases", func(c context.Context) error {
			hits = h.canvasDelta(c, canvasSince(p.afterTS, p.canvasHours, nowUnix), selfID)
			return c.Err()
		})
		if len(hits) > 0 {
			canvasBlock = renderCanvasDelta(hits, nowUnix)
		}
	}

	if p.mentionsOnly {
		if selfID == "" {
			return "", "", errors.New("mentions_only requires auth.test to succeed; got an empty self id")
		}
		results = filterMentions(results, selfID)
	}

	// Delta cursor: drop everything at or before `after` so a same-day
	// re-pull returns only what arrived since. Applied after the DM /
	// thread-mention merges so it prunes those too.
	results = filterAfter(results, p.afterTS)

	// Answered-DM suppression: a DM where the operator holds the last
	// word is a conversation they've already handled — Slack's last_read
	// just hasn't caught up (it updates on client focus, not on send, so
	// replying from a notification leaves the DM "unread" for minutes).
	// Probe each DM's actual newest message and drop the answered ones.
	// Skipped when dm_window_hours is on (that mode explicitly asks for
	// already-read DM recaps) or when the caller wants them shown.
	var answeredNote string
	if !p.showAnswered && p.dmWindowHours <= 0 && selfID != "" {
		recentFn := func(ctx context.Context, channelID string) ([]goslack.Message, error) {
			return h.Messages().History(ctx, slack.HistoryParams{ChannelID: channelID, Limit: answeredDMWindow})
		}
		var answered []*slack.ChannelUnread
		// Display-only: a DM the probe could not check stays visible.
		_ = sw.runDisplay("answered-dms", func(c context.Context) error {
			results, answered = dropAnsweredDMs(c, recentFn, selfID, results)
			return c.Err()
		})
		if len(answered) > 0 {
			labels := make([]string, 0, len(answered))
			for _, cu := range answered {
				labels = append(labels, channelDisplayLabel(ctx, cu.Channel, h.Users()))
			}
			answeredNote = fmt.Sprintf("%d answered DM(s) hidden (you have the last word): %s — pass show_answered=true to include\n",
				len(answered), strings.Join(labels, ", "))
		}
	}

	deadlineNote := sw.note(true)

	if len(results) == 0 {
		// No unread messages, but a canvas may still have changed — report
		// it rather than claiming "all caught up".
		tail := strings.TrimRight(deadlineNote+priorityNote+answeredNote+canvasBlock, "\n")
		if tail != "" {
			return tail, p.afterTS, nil
		}
		// No new content: carry the caller's incoming cursor forward so a
		// combined cursor never regresses for a caught-up workspace.
		return "", p.afterTS, nil
	}

	now := time.Now()
	sort.SliceStable(results, func(i, j int) bool {
		// Priority channels lead regardless of score: the operator named
		// them, and under max_chars the tail is what gets dropped.
		if results[i].Priority != results[j].Priority {
			return results[i].Priority
		}
		return digest.RankUnread(results[i], selfID, now, p.urg) > digest.RankUnread(results[j], selfID, now, p.urg)
	})

	totalMsgs, totalReplies := 0, 0
	for _, r := range results {
		totalMsgs += len(r.Messages)
		for _, rs := range r.Replies {
			totalReplies += len(rs)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d channels, %d top-level + %d thread replies\n",
		len(results), totalMsgs, totalReplies)
	b.WriteString(deadlineNote)
	if priorityNote != "" {
		b.WriteString(priorityNote)
	}
	if answeredNote != "" {
		b.WriteString(answeredNote)
	}
	// Emit the delta cursor so the next call can pass after=<ts> and get
	// only newer messages. One cheap line replaces the whole References
	// block as the default machine-readable handle on this pull. The same
	// value is returned to the caller, which in multi-workspace mode folds
	// it into the combined per-workspace cursor token.
	cursor = newestTS(results)
	if sw.incomplete() {
		cursor = p.afterTS
	}
	if cursor != "" {
		fmt.Fprintf(&b, "cursor: %s (pass as after= next pull for a delta)\n", cursor)
	}
	if canvasBlock != "" {
		b.WriteString(canvasBlock)
	}
	b.WriteByte('\n')

	logChannels := 0
	var dropped []string
	for _, r := range results {
		users := h.resolveRefsWithReplies(ctx, r)
		label := channelDisplayLabel(ctx, r.Channel, h.Users())
		if r.Priority {
			label = priorityLabel(label, r)
		}
		isGit := p.logMode != "off" && digest.DetectGitChannel(r)
		isLog := !isGit && p.logMode != "off" && digest.DetectLogChannel(r)
		lowSignal := !isGit && !isLog && !r.Priority && digest.DetectLowSignalChannel(r)
		// skip_git_mode / skip_log_mode hide the bot feed, not a human
		// reply in it: a channel that carries thread replies keeps them.
		skipped := (p.skipGit && isGit) || (p.skipLog && isLog)
		if skipped && len(r.Replies) == 0 {
			continue
		}
		var rendered string
		switch {
		case isGit:
			logChannels++
			// An empty git feed renders nothing, but its thread replies
			// still do (below) — no `continue` here.
			if workflows, orphans := digest.GroupGitWorkflows(r.Messages); len(workflows) > 0 || len(orphans) > 0 {
				rendered = digest.RenderGitChannel(label, len(r.Messages), workflows, orphans)
			}
		case isLog:
			logChannels++
			bands := digest.BuildLogBands(r.Messages, p.logSamples)
			rendered = format.LogChannelDigest(label, len(r.Messages), bands, users)
		case lowSignal:
			rendered = digest.RenderLowSignalChannel(label, r)
		default:
			chOpts := []format.DigestOption{
				format.WithMentionHighlight(selfID),
				format.WithThreadReplies(r.Replies),
				format.WithThreadPreviewReplies(p.replyCap),
				format.WithOmitEmpty(),
			}
			if slack.IsDirectMessage(r.Channel) {
				// DMs are the actionable layer — few, human-length, and the
				// place amounts/deadlines/asks live. Render them fuller than
				// the 280-char channel preview so the sweep stops truncating
				// "(+866 chars)" on the one line that mattered. Bounded (not
				// full_text) so a wall-of-text paste can't blow the budget
				// and get the whole DM dropped.
				if p.dmFullText {
					chOpts = append(chOpts, format.WithMessageLimit(dmMessageLimit))
				}
			} else {
				// Collapse huddle noise in channels (standup/ad-hoc call
				// pings); huddles stay inline in DMs, where the call is the
				// signal.
				chOpts = append(chOpts, format.WithHuddleAggregation())
			}
			rendered = format.ChannelDigest(label, r.Messages, users, p.maxPer, chOpts...)
		}
		// Summarising renderers (git, log, low-signal) describe the
		// top-level messages and used to drop thread replies with them —
		// including a human answer in a thread the operator is part of,
		// brought in by the own-thread or mention backstops. Keep them.
		// ADR 123.
		if skipped {
			rendered = ""
		}
		if (isGit || isLog || lowSignal) && len(r.Replies) > 0 {
			if block := format.ThreadRepliesBlock(r.Replies, users,
				format.WithMentionHighlight(selfID),
				format.WithThreadPreviewReplies(p.replyCap)); block != "" {
				if rendered == "" {
					rendered = "## " + label
				}
				rendered += "\n" + block
			}
		}
		if rendered == "" {
			continue
		}
		if !budgetAppend(&b, rendered, p.maxChars) {
			// Carry the volume: a bare name doesn't say whether the
			// channel is worth a drill-in call.
			dropped = append(dropped, fmt.Sprintf("%s (%d)", label, len(r.Messages)))
			continue
		}
	}
	if logChannels > 0 {
		h.log.Debug("log mode applied", "channels", logChannels)
	}
	if len(dropped) > 0 {
		fmt.Fprintf(&b, "+ %d channels omitted by max_chars cap, with unread counts: %s\n  (use get_channel_digest to drill in)\n\n",
			len(dropped), strings.Join(dropped, ", "))
	}
	if p.includeRefs {
		if footer := digest.RenderReferences(digest.CollectReferences(results)); footer != "" {
			b.WriteString(footer)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), cursor, nil
}

// sweepMentions renders the mentions sweep for the single workspace this
// Hub is scoped to, its fetch phases run through sw (ADR 121). Returns
// the rendered block (including its own count header) or "" when there
// are no qualifying mentions. A search query or the DM backstop cut by
// the deadline makes the list partial — said so at the top, never
// rendered as "no mentions".
func (h *Hub) sweepMentions(ctx context.Context, sw *sweep, p mentionParams) (string, error) {
	after := time.Now().Add(-time.Duration(p.hours) * time.Hour).Format("2006-01-02")

	selfID, _ := h.Unread().Self(ctx)
	handle, _ := h.Unread().SelfHandle(ctx)

	// `to:me` matches DMs only, so a channel message tagging the operator
	// never reached this list — the tool answered "no mentions" while the
	// mention sat in a channel. Both queries run and merge; see
	// slack.MentionQueries for why neither one alone is enough.
	var matches []goslack.SearchMessage
	for i, q := range slack.MentionQueries(handle, after) {
		var hits []goslack.SearchMessage
		err := sw.run(fmt.Sprintf("search-%d", i+1), func(c context.Context) error {
			r, e := h.Search().Messages(c, q, p.limit)
			hits = r
			return e
		})
		if err != nil {
			if sw.incomplete() {
				break
			}
			return "", err
		}
		matches = mergeSearchHits(matches, hits)
	}

	// The handle query also returns the operator's own messages; those
	// are not mentions of them.
	matches = filterOwnMessages(matches, selfID)

	// DM history backstop: Slack's search index lags on DMs, so a message
	// the other party sent minutes ago is often missing from `to:me`.
	// RecentDMActivity reads DM history directly (real-time, no index
	// lag); fold its fresh messages in as synthetic hits (deduped by
	// channel+ts, re-sorted newest-first) so get_mentions stops silently
	// missing just-arrived DMs. Needs self id to exclude our own lines.
	if p.dmHistory && selfID != "" {
		var dmCus []*slack.ChannelUnread
		if dmErr := sw.run("dm-history", func(c context.Context) error {
			r, e := h.Unread().RecentDMActivity(c, p.hours, p.limit)
			dmCus = r
			return e
		}); dmErr != nil {
			h.log.Warn("mentions dm-history backstop failed; fresh DMs may lag", "err", dmErr)
		} else if hits := dmActivityToHits(dmCus, selfID); len(hits) > 0 {
			matches = mergeSearchHits(matches, hits)
		}
	}

	// Automation senders (calendar/Slackbot/Drive) can't be replied to —
	// drop them from every mentions sweep, not just pending_only. See ADR 021.
	matches = filterBotSenders(matches)

	if p.pendingOnly {
		if selfID == "" {
			return "", errors.New("pending_only requires auth.test to succeed; got an empty self id")
		}
		matches = filterEmptyMentions(matches)
		if p.dropAcks {
			matches = filterClosingAcks(matches)
		}
		// Display-only: a mention whose reply check did not run stays listed.
		_ = sw.runDisplay("pending-check", func(c context.Context) error {
			matches = h.filterPendingMentions(c, matches, selfID)
			return c.Err()
		})
	}
	if p.strictMention {
		if selfID == "" {
			return "", errors.New("strict_mention requires auth.test to succeed; got an empty self id")
		}
		matches = filterStrictMentions(matches, selfID)
	}

	// `limit` caps the merged set, not each query — two searches feed it
	// now, and the union is what the caller asked to be bounded. Ordering
	// is newest-first (mergeSearchHits), so the cap drops the oldest.
	if p.limit > 0 && len(matches) > p.limit {
		matches = matches[:p.limit]
	}

	deadlineNote := sw.note(false)
	if len(matches) == 0 {
		// An empty list from a cut sweep is not "no mentions".
		return strings.TrimRight(deadlineNote, "\n"), nil
	}

	// Summary mode: the operational-load view — aggregates over the same
	// filtered match set the list view would render, so pending_only /
	// strict_mention / dm_history all compose identically.
	if p.summaryOnly {
		return deadlineNote + summarizeMentions(matches, p.hours, p.pendingOnly), nil
	}

	var b strings.Builder
	header := fmt.Sprintf("%d mentions (last %dh)", len(matches), p.hours)
	if p.pendingOnly {
		header += " — pending (no text reply from you)"
	}
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(deadlineNote)
	shownContext := map[string]struct{}{}
	for _, m := range matches {
		b.WriteString(format.SearchResult(m))
		b.WriteByte('\n')
		if p.withContext && m.Channel.ID != "" {
			before, after := h.fetchMentionContext(ctx, m.Channel.ID, m.Timestamp, p.ctxN)
			users := h.Users().NamesFor(ctx, append(collectUserIDs(before), collectUserIDs(after)...))
			writeContextLines(&b, "    ↳ ", before, users, m.Channel.ID, shownContext)
			writeContextLines(&b, "    ↪ ", after, users, m.Channel.ID, shownContext)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// workspaceSection prefixes a rendered body with its workspace heading.
func workspaceSection(name, body string) string {
	return fmt.Sprintf("## [%s]\n%s", name, body)
}

// unknownWorkspaceMsg formats the error when a `workspace` argument does
// not match any configured label.
func unknownWorkspaceMsg(name string, configured []string) string {
	return fmt.Sprintf("unknown workspace %q; configured: %s", name, strings.Join(configured, ", "))
}

// unreadEmptyMsg is the caught-up text for an empty unread sweep.
func unreadEmptyMsg(mentionsOnly bool) string {
	if mentionsOnly {
		return "no unread channels mention you"
	}
	return "all caught up — 0 unread"
}

// mentionsEmptyMsg is the no-results text for an empty mentions sweep.
func mentionsEmptyMsg(pendingOnly bool, hours int) string {
	if pendingOnly {
		return fmt.Sprintf("no pending mentions in last %dh — every direct ask got a text reply from you", hours)
	}
	return fmt.Sprintf("no mentions in last %dh", hours)
}

// mergeDMOverride combines the regular unread sweep with the DM
// time-window override. DM entries from the override replace any
// same-channel entries in `base`; non-DM entries in `base` are
// preserved untouched. DMs that didn't have unread but did have
// recent activity (the whole point of the override) are appended.
//
// The merge is stable in the sense that channels appear in the
// order: first the rewritten/preserved entries from `base`, then
// any DM-only additions from `override` that didn't already exist
// in `base`. The downstream urgency ranker re-orders anyway, so
// strict ordering here is not load-bearing.
func mergeDMOverride(base, override []*slack.ChannelUnread) []*slack.ChannelUnread {
	if len(override) == 0 {
		return base
	}
	byID := make(map[string]*slack.ChannelUnread, len(override))
	for _, o := range override {
		if o == nil || o.Channel.ID == "" {
			continue
		}
		byID[o.Channel.ID] = o
	}
	out := make([]*slack.ChannelUnread, 0, len(base)+len(override))
	seen := make(map[string]struct{}, len(base))
	for _, b := range base {
		if b == nil {
			continue
		}
		// Trust the override side: RecentDMActivity already filtered
		// to IM/MPIM channels, so any match in byID is a DM entry that
		// should replace the truncated base view. Relying on the base
		// channel's IsIM/IsMpIM flag was the v0.4.7 over-defensive
		// check that caused silent misses — users.conversations doesn't
		// always populate those flags for read-state-stale DMs, and a
		// missing flag meant a real DM kept its old unread-only view
		// instead of being refreshed by the time-window fetch.
		if replacement, ok := byID[b.Channel.ID]; ok {
			out = append(out, replacement)
		} else {
			out = append(out, b)
		}
		seen[b.Channel.ID] = struct{}{}
	}
	for _, o := range override {
		if o == nil {
			continue
		}
		if _, dup := seen[o.Channel.ID]; dup {
			continue
		}
		out = append(out, o)
	}
	return out
}

// mergeThreadMentions folds search-based thread-mention hits into the
// regular unread sweep. Unlike mergeDMOverride, this never *replaces*
// an existing entry — it augments. When the channel is already in
// `base` (the unread sweep found other activity there), the mention's
// reply messages are appended into `Replies[threadTS]`. When the
// channel is new (base didn't know about it because the parent was
// already read), the whole `*ChannelUnread` is appended.
//
// Deduplication is by (threadTS, timestamp) so re-runs don't pile up
// duplicate replies if a Slack search returns the same message twice
// across sweeps.
func mergeThreadMentions(base, mentions []*slack.ChannelUnread) []*slack.ChannelUnread {
	if len(mentions) == 0 {
		return base
	}
	byID := make(map[string]*slack.ChannelUnread, len(base))
	for _, b := range base {
		if b == nil || b.Channel.ID == "" {
			continue
		}
		byID[b.Channel.ID] = b
	}
	for _, m := range mentions {
		if m == nil || m.Channel.ID == "" {
			continue
		}
		existing, ok := byID[m.Channel.ID]
		if !ok {
			base = append(base, m)
			byID[m.Channel.ID] = m
			continue
		}
		// Merge top-level messages with timestamp dedup.
		seen := make(map[string]struct{}, len(existing.Messages))
		for _, x := range existing.Messages {
			seen[x.Timestamp] = struct{}{}
		}
		for _, msg := range m.Messages {
			if _, dup := seen[msg.Timestamp]; dup {
				continue
			}
			existing.Messages = append(existing.Messages, msg)
		}
		// Merge thread replies into existing buckets with ts dedup.
		if existing.Replies == nil && len(m.Replies) > 0 {
			existing.Replies = make(map[string][]goslack.Message)
		}
		for threadTS, reps := range m.Replies {
			seenReps := make(map[string]struct{}, len(existing.Replies[threadTS]))
			for _, x := range existing.Replies[threadTS] {
				seenReps[x.Timestamp] = struct{}{}
			}
			for _, r := range reps {
				if _, dup := seenReps[r.Timestamp]; dup {
					continue
				}
				existing.Replies[threadTS] = append(existing.Replies[threadTS], r)
			}
		}
	}
	return base
}

// budgetAppend writes rendered (plus the inter-channel "\n\n" separator)
// to b if doing so wouldn't exceed maxChars. Returns true when the
// channel was emitted, false when it was dropped by the cap.
//
// maxChars==0 disables the cap entirely (the historical behaviour).
// The +2 accounts for the trailing "\n\n" that follows every rendered
// channel — without it we would write a channel that pushes the body
// past the cap *after* the separator.
func budgetAppend(b *strings.Builder, rendered string, maxChars int) bool {
	if maxChars > 0 && b.Len()+len(rendered)+2 > maxChars {
		return false
	}
	b.WriteString(rendered)
	b.WriteString("\n\n")
	return true
}

// filterEmptyMentions drops matches whose body has no real text. An
// empty mention can't be "pending" — there was nothing to reply to.

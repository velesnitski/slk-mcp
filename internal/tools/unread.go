package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
				mcp.WithNumber("log_samples_per_band", mcp.Description("Max sample messages shown per severity band in log mode (default: 1; raise for more inline samples)")),
				mcp.WithBoolean("skip_log_mode", mcp.Description("If true, omit log-mode channels (alert/error feeds) entirely. Cheap way to shrink the output when bot channels dominate (default: false)")),
				mcp.WithBoolean("skip_git_mode", mcp.Description("If true, omit git-mode channels (CI / git-bot feeds) entirely. Cheap way to shrink the output when git activity dominates (default: false)")),
				mcp.WithNumber("max_chars", mcp.Description("Soft cap on rendered body size (in characters). Channels are emitted in urgency order; once the cap is reached, remaining channels are listed in a footer instead of inlined. 0 = unlimited (default).")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				maxPer := int(req.GetFloat("max_per_channel", 20))
				mentionsOnly := req.GetBool("mentions_only", false)
				replyCap := int(req.GetFloat("thread_preview_replies", float64(format.ThreadPreviewReplies)))
				logMode := strings.ToLower(strings.TrimSpace(req.GetString("log_mode", "auto")))
				logSamples := int(req.GetFloat("log_samples_per_band", 1))
				skipLog := req.GetBool("skip_log_mode", false)
				skipGit := req.GetBool("skip_git_mode", false)
				maxChars := int(req.GetFloat("max_chars", 0))
				urg := digest.UrgencyOpts{
					Weight:        req.GetFloat("urgency_weight", 0),
					ExtraKeywords: digest.ParseExtraKeywords(req.GetString("urgency_keywords", "")),
				}

				results, err := h.Unread().UnreadAll(ctx, maxPer)
				if err != nil {
					if errors.Is(err, slack.ErrNoUserToken) {
						return mcp.NewToolResultError(err.Error()), nil
					}
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Best-effort self-resolution for mention markers; a
				// failure here disables highlighting AND mentions_only
				// filtering (we can't filter what we can't identify).
				selfID, err := h.Unread().Self(ctx)
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
				var dropped []string
				for _, r := range results {
					users := h.resolveRefsWithReplies(ctx, r)
					label := channelDisplayLabel(ctx, r.Channel, h.Users())
					isGit := logMode != "off" && digest.DetectGitChannel(r)
					isLog := !isGit && logMode != "off" && digest.DetectLogChannel(r)
					if skipGit && isGit {
						continue
					}
					if skipLog && isLog {
						continue
					}
					var rendered string
					switch {
					case isGit:
						logChannels++
						workflows, orphans := digest.GroupGitWorkflows(r.Messages)
						if len(workflows) == 0 && len(orphans) == 0 {
							continue
						}
						rendered = digest.RenderGitChannel(label, len(r.Messages), workflows, orphans)
					case isLog:
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
					if !budgetAppend(&b, rendered, maxChars) {
						dropped = append(dropped, label)
						continue
					}
				}
				if logChannels > 0 {
					h.log.Debug("log mode applied", "channels", logChannels)
				}
				if len(dropped) > 0 {
					fmt.Fprintf(&b, "+ %d channels omitted by max_chars cap: %s\n  (use get_channel_digest to drill in)\n\n",
						len(dropped), strings.Join(dropped, ", "))
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

				matches, err := h.Search().Messages(ctx, q, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				selfID, _ := h.Unread().Self(ctx)

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
						users := h.Users().NamesFor(ctx, append(collectUserIDs(before), collectUserIDs(after)...))
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

				channelID, err := h.Channels().ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := h.Unread().MarkRead(ctx, channelID, ts); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("marked #%s read up to %s", channel, ts)), nil
			},
		)
	}
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

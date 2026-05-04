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
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func registerUnreadTools(s *server.MCPServer, d Deps) {
	if !d.Client.HasUserToken() {
		d.Log.Info("user token not set; unread/mentions tools disabled",
			"hint", "set SLACK_USER_TOKEN=xoxp-... to enable")
		return
	}

	if !d.Cfg.IsDisabled("get_unread_summary") {
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
				urg := urgencyOpts{
					Weight:        req.GetFloat("urgency_weight", 0),
					ExtraKeywords: parseExtraKeywords(req.GetString("urgency_keywords", "")),
				}

				results, err := d.Client.Unread.UnreadAll(ctx, maxPer)
				if err != nil {
					if errors.Is(err, slack.ErrNoUserToken) {
						return mcp.NewToolResultError(err.Error()), nil
					}
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Best-effort self-resolution for mention markers; a
				// failure here disables highlighting AND mentions_only
				// filtering (we can't filter what we can't identify).
				selfID, err := d.Client.Unread.Self(ctx)
				if err != nil {
					d.Log.Warn("auth.test failed; mention highlighting disabled", "err", err)
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
					return rankUnread(results[i], selfID, now, urg) > rankUnread(results[j], selfID, now, urg)
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
					users := d.Client.Users.NamesFor(ctx, collectUserIDsWithReplies(r))
					label := channelDisplayLabel(ctx, r.Channel, d.Client.Users)
					var rendered string
					switch {
					case logMode != "off" && detectGitChannel(r):
						logChannels++
						workflows, orphans := groupGitWorkflows(r.Messages)
						if len(workflows) == 0 && len(orphans) == 0 {
							continue
						}
						rendered = renderGitChannel(label, len(r.Messages), workflows, orphans)
					case logMode != "off" && detectLogChannel(r):
						logChannels++
						bands := buildLogBands(r.Messages, logSamples)
						rendered = format.LogChannelDigest(label, len(r.Messages), bands, users)
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
					d.Log.Debug("log mode applied", "channels", logChannels)
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("get_mentions") {
		s.AddTool(
			mcp.NewTool("get_mentions",
				mcp.WithDescription("Messages that mention the authenticated user. Requires SLACK_USER_TOKEN."),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: 72)")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
				mcp.WithBoolean("with_context", mcp.Description("For each hit, fetch a few preceding messages from the same channel/DM (default: false)")),
				mcp.WithNumber("context_messages", mcp.Description("How many preceding messages to inline when with_context=true (default: 3)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				hours := int(req.GetFloat("hours", 72))
				limit := int(req.GetFloat("limit", 30))
				withContext := req.GetBool("with_context", false)
				ctxN := int(req.GetFloat("context_messages", 3))

				after := time.Now().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02")
				q := fmt.Sprintf("to:me after:%s", after)

				matches, err := d.Client.Search.Messages(ctx, q, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if len(matches) == 0 {
					return mcp.NewToolResultText(fmt.Sprintf("no mentions in last %dh", hours)), nil
				}

				var b strings.Builder
				fmt.Fprintf(&b, "%d mentions (last %dh)\n", len(matches), hours)
				for _, m := range matches {
					b.WriteString(format.SearchResult(m))
					b.WriteByte('\n')
					if withContext && m.Channel.ID != "" {
						before, after := fetchMentionContext(ctx, d, m.Channel.ID, m.Timestamp, ctxN)
						users := d.Client.Users.NamesFor(ctx, append(collectUserIDs(before), collectUserIDs(after)...))
						for _, p := range before {
							b.WriteString("    ↳ ")
							b.WriteString(format.MessageLine(p, users[p.User]))
							b.WriteByte('\n')
						}
						for _, p := range after {
							b.WriteString("    ↪ ")
							b.WriteString(format.MessageLine(p, users[p.User]))
							b.WriteByte('\n')
						}
					}
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !d.Cfg.ReadOnly && !d.Cfg.IsDisabled("mark_read") {
		s.AddTool(
			mcp.NewTool("mark_read",
				mcp.WithDescription("Mark a channel as read up to a given message timestamp."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("timestamp", mcp.Required(), mcp.Description("Message ts to mark read through")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				ts, err := req.RequireString("timestamp")
				if err != nil {
					return mcp.NewToolResultError("timestamp is required"), nil
				}
				channelID, err := d.Client.Channels.ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := d.Client.Unread.MarkRead(ctx, channelID, ts); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("marked #%s read up to %s", channel, ts)), nil
			},
		)
	}
}

// fetchMentionContext returns up to n messages on each side of
// `pivotTS` in `channelID`, ordered oldest → newest. Both lists are
// best-effort and may be empty on error.
func fetchMentionContext(ctx context.Context, d Deps, channelID, pivotTS string, n int) (before, after []goslack.Message) {
	if n <= 0 {
		n = 3
	}
	hist, err := d.Client.Messages.History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		Limit:     n + 1,
	})
	if err != nil {
		d.Log.Debug("fetch mention context (before) failed", "channel", channelID, "err", err)
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
		hist, err := d.Client.Messages.History(ctx, slack.HistoryParams{
			ChannelID: channelID,
			OldestTS:  pivot,
			Limit:     n + 1,
		})
		if err != nil {
			d.Log.Debug("fetch mention context (after) failed", "channel", channelID, "err", err)
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

// rankUnread orders ChannelUnread results. The components, in order
// of dominance:
//
//  1. A direct mention of selfID — adds 1_000_000, so any mention
//     beats any volume or urgency from non-mentioning channels.
//  2. Urgency heuristic — keywords ("urgent" / "срочно"), question
//     marks, urgency reactions, recency. See internal/tools/urgency.go.
//     Tunable per call via urgencyOpts (weight + extra keywords).
//  3. Raw volume — total unread messages + replies.
//
// `now` is injected so tests can fix recency. Pass time.Time{} to
// disable the recency component.
func rankUnread(cu *slack.ChannelUnread, selfID string, now time.Time, urg urgencyOpts) int {
	rank := len(cu.Messages)
	for _, rs := range cu.Replies {
		rank += len(rs)
	}
	rank += urgencyScore(cu, now, urg)
	if channelMentions(cu, selfID) {
		rank += 1_000_000
	}
	return rank
}

func channelMentions(cu *slack.ChannelUnread, selfID string) bool {
	if selfID == "" {
		return false
	}
	for _, m := range cu.Messages {
		if format.MentionsUser(m, selfID) {
			return true
		}
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			if format.MentionsUser(r, selfID) {
				return true
			}
		}
	}
	return false
}

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
		if channelMentions(r, selfID) {
			out = append(out, r)
		}
	}
	return out
}

// collectUserIDsWithReplies returns unique user IDs across both the
// channel's top-level unread messages and any inlined thread replies.
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
	return ids
}

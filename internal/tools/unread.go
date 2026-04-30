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
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				maxPer := int(req.GetFloat("max_per_channel", 20))
				mentionsOnly := req.GetBool("mentions_only", false)
				replyCap := int(req.GetFloat("thread_preview_replies", float64(format.ThreadPreviewReplies)))
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

				for _, r := range results {
					users := d.Client.Users.NamesFor(ctx, collectUserIDsWithReplies(r))
					b.WriteString(format.ChannelDigest(
						r.Channel.Name, r.Messages, users, maxPer,
						format.WithMentionHighlight(selfID),
						format.WithThreadReplies(r.Replies),
						format.WithThreadPreviewReplies(replyCap),
					))
					b.WriteString("\n\n")
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
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				hours := int(req.GetFloat("hours", 72))
				limit := int(req.GetFloat("limit", 30))

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

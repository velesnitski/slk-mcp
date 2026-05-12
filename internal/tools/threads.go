package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// buildUserMessagesQuery assembles the Slack search query for
// get_user_messages. Factored out for unit testing so the date-bound
// behaviour stays pinned even if the surrounding handler shifts.
//
// since/until pass straight through to Slack's own after:/before:
// operators. Validation (date format) is the caller's job.
func buildUserMessagesQuery(user, channel, since, until string) string {
	parts := []string{"from:@" + user}
	if channel != "" {
		parts = append(parts, "in:#"+strings.TrimPrefix(channel, "#"))
	}
	if since != "" {
		parts = append(parts, "after:"+since)
	}
	if until != "" {
		parts = append(parts, "before:"+until)
	}
	return strings.Join(parts, " ")
}

func (h *Hub) registerThreadTools(s *server.MCPServer) {
	if !h.cfg.IsDisabled("get_thread") {
		s.AddTool(
			mcp.NewTool("get_thread",
				mcp.WithDescription("Fetch all replies in a thread. Pass either (channel + thread_ts) or a Slack permalink."),
				mcp.WithString("channel", mcp.Description("Channel name (optional if permalink is provided)")),
				mcp.WithString("thread_ts", mcp.Description("Thread root timestamp (optional if permalink is provided)")),
				mcp.WithString("permalink", mcp.Description("Slack permalink to any message in the thread — fills channel and thread_ts in one go")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel := req.GetString("channel", "")
				threadTS := req.GetString("thread_ts", "")
				permalink := req.GetString("permalink", "")

				if permalink != "" {
					p, err := slack.ParseSlackPermalink(permalink)
					if err != nil {
						return mcp.NewToolResultError("permalink could not be parsed: " + err.Error()), nil
					}
					if p != nil {
						// Explicit args still win when both are passed; permalink
						// only fills what the caller did not provide.
						if channel == "" {
							channel = p.ChannelID
						}
						if threadTS == "" {
							threadTS = p.ThreadTS
						}
					}
				}

				if channel == "" {
					return mcp.NewToolResultError("channel is required (or pass a permalink)"), nil
				}
				if threadTS == "" {
					return mcp.NewToolResultError("thread_ts is required (or pass a permalink)"), nil
				}

				channelID, err := h.Channels().ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				replies, err := h.Messages().ThreadReplies(ctx, channelID, threadTS)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				users := h.resolveRefs(ctx, replies)

				var b strings.Builder
				fmt.Fprintf(&b, "thread #%s (%d msgs)\n", channel, len(replies))
				for _, m := range replies {
					b.WriteString(format.MessageLine(m, users[m.User]))
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_user_messages") {
		s.AddTool(
			mcp.NewTool("get_user_messages",
				mcp.WithDescription("Recent messages from a user. Uses workspace search. "+
					"Pass since=/until= (YYYY-MM-DD) for absolute-time scans — preferred over "+
					"get_unread_summary when verifying that a user posted by a deadline, since "+
					"unread state depends on the caller's last_read mark."),
				mcp.WithString("user", mcp.Required(), mcp.Description("Username or display name")),
				mcp.WithString("channel", mcp.Description("Optional channel name to restrict search")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
				mcp.WithString("since", mcp.Description("Lower bound, YYYY-MM-DD. Maps to Slack search after:")),
				mcp.WithString("until", mcp.Description("Upper bound, YYYY-MM-DD. Maps to Slack search before:")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				user, err := req.RequireString("user")
				if err != nil {
					return mcp.NewToolResultError("user is required"), nil
				}
				channel := req.GetString("channel", "")
				limit := int(req.GetFloat("limit", 30))
				since := req.GetString("since", "")
				until := req.GetString("until", "")
				for _, d := range []struct{ name, val string }{{"since", since}, {"until", until}} {
					if d.val == "" {
						continue
					}
					if _, perr := time.Parse("2006-01-02", d.val); perr != nil {
						return mcp.NewToolResultError(d.name + " must be YYYY-MM-DD"), nil
					}
				}

				query := buildUserMessagesQuery(user, channel, since, until)
				matches, err := h.Search().Messages(ctx, query, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if len(matches) == 0 {
					return mcp.NewToolResultText(fmt.Sprintf("no messages from %s", user)), nil
				}

				var b strings.Builder
				fmt.Fprintf(&b, "%d msgs from %s\n", len(matches), user)
				for _, m := range matches {
					b.WriteString(format.SearchResult(m))
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if h.cfg.ReadOnly {
		return
	}

	if !h.cfg.IsDisabled("post_message") {
		s.AddTool(
			mcp.NewTool("post_message",
				mcp.WithDescription("Post a message to a channel. Supports thread replies."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("text", mcp.Required(), mcp.Description("Message text (Slack markdown)")),
				mcp.WithString("thread_ts", mcp.Description("Optional thread timestamp to reply in thread")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				text, err := req.RequireString("text")
				if err != nil {
					return mcp.NewToolResultError("text is required"), nil
				}
				threadTS := req.GetString("thread_ts", "")

				channelID, err := h.Channels().ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				ts, err := h.Messages().Post(ctx, channelID, text, threadTS)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("posted to #%s (ts: %s)", channel, ts)), nil
			},
		)
	}

	if !h.cfg.IsDisabled("add_reaction") {
		s.AddTool(
			mcp.NewTool("add_reaction",
				mcp.WithDescription("Add an emoji reaction to a message."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("timestamp", mcp.Required(), mcp.Description("Message ts")),
				mcp.WithString("emoji", mcp.Required(), mcp.Description("Emoji name without colons (e.g. thumbsup)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, _ := req.RequireString("channel")
				timestamp, _ := req.RequireString("timestamp")
				emoji, _ := req.RequireString("emoji")

				channelID, err := h.Channels().ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := h.Messages().AddReaction(ctx, channelID, timestamp, emoji); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("added :%s: on #%s", emoji, channel)), nil
			},
		)
	}
}

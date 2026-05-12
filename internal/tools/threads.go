package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/format"
)

func registerThreadTools(s *server.MCPServer, d Deps) {
	if !d.Cfg.IsDisabled("get_thread") {
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
					p, err := parseSlackPermalink(permalink)
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

				channelID, err := d.Client.Channels.ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				replies, err := d.Client.Messages.ThreadReplies(ctx, channelID, threadTS)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				users := resolveRefs(ctx, d, replies)

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

	if !d.Cfg.IsDisabled("get_user_messages") {
		s.AddTool(
			mcp.NewTool("get_user_messages",
				mcp.WithDescription("Recent messages from a user. Uses workspace search."),
				mcp.WithString("user", mcp.Required(), mcp.Description("Username or display name")),
				mcp.WithString("channel", mcp.Description("Optional channel name to restrict search")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				user, err := req.RequireString("user")
				if err != nil {
					return mcp.NewToolResultError("user is required"), nil
				}
				channel := req.GetString("channel", "")
				limit := int(req.GetFloat("limit", 30))

				query := "from:@" + user
				if channel != "" {
					query += " in:#" + strings.TrimPrefix(channel, "#")
				}
				matches, err := d.Client.Search.Messages(ctx, query, limit)
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

	if d.Cfg.ReadOnly {
		return
	}

	if !d.Cfg.IsDisabled("post_message") {
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

				channelID, err := d.Client.Channels.ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				ts, err := d.Client.Messages.Post(ctx, channelID, text, threadTS)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("posted to #%s (ts: %s)", channel, ts)), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("add_reaction") {
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

				channelID, err := d.Client.Channels.ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := d.Client.Messages.AddReaction(ctx, channelID, timestamp, emoji); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("added :%s: on #%s", emoji, channel)), nil
			},
		)
	}
}

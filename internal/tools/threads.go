package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	slk "github.com/velesnitski/slk-mcp/internal/slack"
)

func registerThreadTools(s *server.MCPServer, client *slk.Client, readOnly bool) {
	s.AddTool(
		mcp.NewTool("get_thread",
			mcp.WithDescription("Get all replies in a thread."),
			mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
			mcp.WithString("thread_ts", mcp.Required(), mcp.Description("Thread timestamp")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channel, _ := req.RequireString("channel")
			threadTS, _ := req.RequireString("thread_ts")

			channelID, err := client.ResolveChannelID(channel)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			replies, err := client.GetThreadReplies(channelID, threadTS)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			var b strings.Builder
			fmt.Fprintf(&b, "**Thread in #%s** (%d messages)\n\n", channel, len(replies))

			for _, msg := range replies {
				name := client.ResolveUserName(msg.User)
				b.WriteString(slk.FormatMessage(msg, name))
				b.WriteString("\n\n")
			}

			return mcp.NewToolResultText(b.String()), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_user_messages",
			mcp.WithDescription("Get recent messages from a specific user."),
			mcp.WithString("user", mcp.Required(), mcp.Description("Username or display name")),
			mcp.WithString("channel", mcp.Description("Optional channel to limit search")),
			mcp.WithNumber("hours", mcp.Description("How far back (default: 24)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			user, _ := req.RequireString("user")
			channel := req.GetString("channel", "")

			query := fmt.Sprintf("from:@%s", user)
			if channel != "" {
				query += fmt.Sprintf(" in:#%s", strings.TrimPrefix(channel, "#"))
			}

			matches, err := client.SearchMessages(query, 50)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			if len(matches) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No messages from %s.", user)), nil
			}

			var b strings.Builder
			fmt.Fprintf(&b, "**Messages from %s** (%d found)\n\n", user, len(matches))

			for _, m := range matches {
				text := m.Text
				if len(text) > 300 {
					text = text[:300]
				}
				ts := slk.ParseTS(m.Timestamp)
				dateStr := ts.Format("2006-01-02 15:04")

				fmt.Fprintf(&b, "**#%s** %s\n%s\n\n", m.Channel.Name, dateStr, text)
			}

			return mcp.NewToolResultText(b.String()), nil
		},
	)

	if !readOnly {
		s.AddTool(
			mcp.NewTool("post_message",
				mcp.WithDescription("Post a message to a channel."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("text", mcp.Required(), mcp.Description("Message text (Slack markdown)")),
				mcp.WithString("thread_ts", mcp.Description("Optional thread timestamp to reply in thread")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, _ := req.RequireString("channel")
				text, _ := req.RequireString("text")
				threadTS := req.GetString("thread_ts", "")

				channelID, err := client.ResolveChannelID(channel)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
				}

				ts, err := client.PostMessage(channelID, text, threadTS)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("Message posted to #%s (ts: %s)", channel, ts)), nil
			},
		)

		s.AddTool(
			mcp.NewTool("add_reaction",
				mcp.WithDescription("Add a reaction emoji to a message."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("timestamp", mcp.Required(), mcp.Description("Message timestamp")),
				mcp.WithString("emoji", mcp.Required(), mcp.Description("Emoji name without colons (e.g. thumbsup)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, _ := req.RequireString("channel")
				timestamp, _ := req.RequireString("timestamp")
				emoji, _ := req.RequireString("emoji")

				channelID, err := client.ResolveChannelID(channel)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
				}

				if err := client.AddReaction(channelID, timestamp, emoji); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("Added :%s: to message in #%s", emoji, channel)), nil
			},
		)
	}
}

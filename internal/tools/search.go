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

func registerSearchTools(s *server.MCPServer, d Deps) {
	if !d.Cfg.IsDisabled("search_messages") {
		s.AddTool(
			mcp.NewTool("search_messages",
				mcp.WithDescription("Workspace search (Slack syntax: from:@user, in:#channel, has:link, before:/after:DATE). Each hit includes thread_ts + permalink so callers can chain into get_thread."),
				mcp.WithString("query", mcp.Required(), mcp.Description("Slack search query")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 20)")),
				mcp.WithBoolean("full_text", mcp.Description("Disable the 200-char body truncation (default: false). Use when issue IDs or URLs sit at the end of the body.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				q, err := req.RequireString("query")
				if err != nil {
					return mcp.NewToolResultError("query is required"), nil
				}
				limit := int(req.GetFloat("limit", 20))
				fullText := req.GetBool("full_text", false)

				matches, err := d.Client.Search.Messages(ctx, q, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if len(matches) == 0 {
					return mcp.NewToolResultText(fmt.Sprintf("no hits for: %s", q)), nil
				}

				var b strings.Builder
				fmt.Fprintf(&b, "%d hits for: %s\n", len(matches), q)
				for _, m := range matches {
					b.WriteString(format.SearchResultExt(m, fullText))
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("find_decisions") {
		s.AddTool(
			mcp.NewTool("find_decisions",
				mcp.WithDescription("Scan channels for messages that look like decisions (keywords + reactions)."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window (default: 72)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				list, _, err := resolveTargetChannels(ctx, d, req.GetString("channels", ""))
				if err != nil {
					return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
				}
				if len(list) == 0 {
					return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
				}
				hours := int(req.GetFloat("hours", 72))
				oldest := time.Now().Add(-time.Duration(hours) * time.Hour)

				var decisions []string
				for _, ch := range list {
					channelID, err := d.Client.Channels.ResolveID(ctx, ch)
					if err != nil {
						decisions = append(decisions, fmt.Sprintf("- #%s error: %v", ch, err))
						continue
					}
					msgs, err := d.Client.Messages.History(ctx, slack.HistoryParams{
						ChannelID: channelID,
						OldestTS:  float64(oldest.Unix()),
						Limit:     d.Cfg.MaxMessagesPerChannel,
					})
					if err != nil {
						decisions = append(decisions, fmt.Sprintf("- #%s error: %v", ch, err))
						continue
					}
					users := d.Client.Users.NamesFor(ctx, collectUserIDs(msgs))
					decisions = append(decisions, detectDecisions(d.Cfg, ch, msgs, users, format.DecisionLine)...)
				}

				if len(decisions) == 0 {
					return mcp.NewToolResultText(fmt.Sprintf("no decisions found in last %dh", hours)), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d decisions (last %dh)\n", len(decisions), hours)
				for _, d := range decisions {
					b.WriteString(d)
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}
}

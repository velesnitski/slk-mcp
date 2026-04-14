package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	slk "github.com/velesnitski/slk-mcp/internal/slack"
)

func registerSearchTools(s *server.MCPServer, client *slk.Client) {
	s.AddTool(
		mcp.NewTool("search_messages",
			mcp.WithDescription("Search messages across all channels. Supports Slack search syntax: from:@user, in:#channel, has:link, before:/after: dates."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
			mcp.WithNumber("limit", mcp.Description("Max results (default: 20)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, err := req.RequireString("query")
			if err != nil {
				return mcp.NewToolResultError("Missing query"), nil
			}
			limit := req.GetFloat("limit", 20)

			matches, err := client.SearchMessages(query, int(limit))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			if len(matches) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No messages found for: %s", query)), nil
			}

			var b strings.Builder
			fmt.Fprintf(&b, "**%d results for:** %s\n\n", len(matches), query)

			for _, m := range matches {
				text := m.Text
				if len(text) > 200 {
					text = text[:200]
				}
				ts := slk.ParseTS(m.Timestamp)
				dateStr := ts.Format("2006-01-02 15:04")

				fmt.Fprintf(&b, "**#%s** %s (%s)\n%s\n\n", m.Channel.Name, dateStr, m.Username, text)
			}

			return mcp.NewToolResultText(b.String()), nil
		},
	)

	s.AddTool(
		mcp.NewTool("find_decisions",
			mcp.WithDescription("Find messages that look like decisions across channels. Detects decision keywords and reactions."),
			mcp.WithString("channels", mcp.Description("Comma-separated channel names. Uses SLACK_CHANNELS if empty.")),
			mcp.WithNumber("hours", mcp.Description("How far back to look (default: 72)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channelsStr := req.GetString("channels", "")
			hours := req.GetFloat("hours", 72)

			channelList := parseChannelList(channelsStr, client.Config().Channels)
			if len(channelList) == 0 {
				return mcp.NewToolResultError("No channels specified. Pass channels or set SLACK_CHANNELS env var."), nil
			}

			oldest := time.Now().Add(-time.Duration(int(hours)) * time.Hour)
			var decisions []string

			for _, chName := range channelList {
				channelID, err := client.ResolveChannelID(chName)
				if err != nil {
					decisions = append(decisions, fmt.Sprintf("- **#%s**: Error: %v", chName, err))
					continue
				}

				messages, err := client.GetChannelHistory(channelID, oldest, 200)
				if err != nil {
					decisions = append(decisions, fmt.Sprintf("- **#%s**: Error: %v", chName, err))
					continue
				}

				userNames := resolveUsers(client, messages)
				found := detectDecisions(client, chName, messages, userNames)
				decisions = append(decisions, found...)
			}

			if len(decisions) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No decisions found in the last %dh.", int(hours))), nil
			}

			return mcp.NewToolResultText(
				fmt.Sprintf("**%d decisions found (%dh)**\n\n%s", len(decisions), int(hours), strings.Join(decisions, "\n")),
			), nil
		},
	)
}

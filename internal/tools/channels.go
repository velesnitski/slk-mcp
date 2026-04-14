package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	slk "github.com/velesnitski/slk-mcp/internal/slack"
)

func registerChannelTools(s *server.MCPServer, client *slk.Client) {
	s.AddTool(
		mcp.NewTool("list_channels",
			mcp.WithDescription("List accessible Slack channels with member counts and topics."),
			mcp.WithNumber("limit", mcp.Description("Max channels to return (default: 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			limit := req.GetFloat("limit", 100)

			channels, err := client.ListChannels(int(limit))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			sort.Slice(channels, func(i, j int) bool {
				return channels[i].NumMembers > channels[j].NumMembers
			})

			var b strings.Builder
			fmt.Fprintf(&b, "**%d channels**\n\n", len(channels))
			for _, ch := range channels {
				topic := ch.Topic.Value
				if len(topic) > 80 {
					topic = topic[:80]
				}
				topicStr := ""
				if topic != "" {
					topicStr = " — " + topic
				}
				fmt.Fprintf(&b, "- **#%s** (%d members)%s\n", ch.Name, ch.NumMembers, topicStr)
			}

			return mcp.NewToolResultText(b.String()), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_channel_info",
			mcp.WithDescription("Get detailed info about a channel (topic, purpose, member count, created date)."),
			mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channel, err := req.RequireString("channel")
			if err != nil {
				return mcp.NewToolResultError("Missing channel"), nil
			}

			channelID, err := client.ResolveChannelID(channel)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			ch, err := client.GetChannelInfo(channelID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}

			created := time.Unix(int64(ch.Created), 0).Format("2006-01-02")

			result := fmt.Sprintf(
				"**#%s**\n- Members: %d\n- Created: %s\n- Topic: %s\n- Purpose: %s\n- Archived: %v",
				ch.Name, ch.NumMembers, created,
				ch.Topic.Value, ch.Purpose.Value, ch.IsArchived,
			)

			return mcp.NewToolResultText(result), nil
		},
	)
}

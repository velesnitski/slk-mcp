package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerChannelTools(s *server.MCPServer, d Deps) {
	if !d.Cfg.IsDisabled("list_channels") {
		s.AddTool(
			mcp.NewTool("list_channels",
				mcp.WithDescription("List Slack channels the bot can see, ordered by member count."),
				mcp.WithNumber("limit", mcp.Description("Max channels to return (default: 100)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				limit := int(req.GetFloat("limit", 100))
				channels, err := d.Client.Channels.List(ctx, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("list channels: %v", err)), nil
				}
				sort.Slice(channels, func(i, j int) bool {
					return channels[i].NumMembers > channels[j].NumMembers
				})

				var b strings.Builder
				fmt.Fprintf(&b, "%d channels\n", len(channels))
				for _, ch := range channels {
					topic := strings.TrimSpace(ch.Topic.Value)
					if len(topic) > 80 {
						topic = topic[:80] + "..."
					}
					if topic != "" {
						fmt.Fprintf(&b, "- #%s (%d) %s\n", ch.Name, ch.NumMembers, topic)
					} else {
						fmt.Fprintf(&b, "- #%s (%d)\n", ch.Name, ch.NumMembers)
					}
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("get_channel_info") {
		s.AddTool(
			mcp.NewTool("get_channel_info",
				mcp.WithDescription("Get a channel's topic, purpose, member count and created date. Optionally lists member display names."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops)")),
				mcp.WithBoolean("include_members", mcp.Description("Resolve and list channel members (default: false)")),
				mcp.WithNumber("members_limit", mcp.Description("Cap on members listed (default: 50, 0 = all)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				name, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				includeMembers := req.GetBool("include_members", false)
				membersLimit := int(req.GetFloat("members_limit", 50))

				channelID, err := d.Client.Channels.ResolveID(ctx, name)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				ch, err := d.Client.Channels.Info(ctx, channelID)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				created := time.Unix(int64(ch.Created), 0).Format("2006-01-02")
				var b strings.Builder
				fmt.Fprintf(&b,
					"#%s\nmembers: %d\ncreated: %s\ntopic: %s\npurpose: %s\narchived: %v",
					ch.Name, ch.NumMembers, created,
					firstLine(ch.Topic.Value), firstLine(ch.Purpose.Value), ch.IsArchived,
				)

				if includeMembers {
					ids, err := d.Client.Channels.Members(ctx, channelID, membersLimit)
					if err != nil {
						fmt.Fprintf(&b, "\nmembers_error: %s", err.Error())
					} else {
						names := d.Client.Users.NamesFor(ctx, ids)
						b.WriteString("\nroster:")
						for _, id := range ids {
							fmt.Fprintf(&b, "\n- %s", names[id])
						}
						if membersLimit > 0 && ch.NumMembers > len(ids) {
							fmt.Fprintf(&b, "\n(+%d more, raise members_limit to see all)", ch.NumMembers-len(ids))
						}
					}
				}
				return mcp.NewToolResultText(b.String()), nil
			},
		)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

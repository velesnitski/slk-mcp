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

func registerDigestTools(s *server.MCPServer, client *slk.Client) {
	s.AddTool(
		mcp.NewTool("get_channel_digest",
			mcp.WithDescription("Get a digest of recent messages from a channel."),
			mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (e.g. devops or #devops)")),
			mcp.WithNumber("hours", mcp.Description("How many hours back (default: 24)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channel, err := req.RequireString("channel")
			if err != nil {
				return mcp.NewToolResultError("Missing channel"), nil
			}
			hours := req.GetFloat("hours", 24)

			result, err := channelDigest(client, channel, int(hours))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_multi_channel_digest",
			mcp.WithDescription("Get a digest across multiple channels. Uses SLACK_CHANNELS if none specified."),
			mcp.WithString("channels", mcp.Description("Comma-separated channel names. If empty, uses SLACK_CHANNELS.")),
			mcp.WithNumber("hours", mcp.Description("How many hours back (default: 24)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channelsStr := req.GetString("channels", "")
			hours := req.GetFloat("hours", 24)

			channelList := parseChannelList(channelsStr, client.Config().Channels)
			if len(channelList) == 0 {
				return mcp.NewToolResultError("No channels specified. Pass channels or set SLACK_CHANNELS env var."), nil
			}

			var parts []string
			for _, ch := range channelList {
				digest, err := channelDigest(client, ch, int(hours))
				if err != nil {
					parts = append(parts, fmt.Sprintf("## #%s\nError: %v", ch, err))
				} else {
					parts = append(parts, digest)
				}
			}

			return mcp.NewToolResultText(strings.Join(parts, "\n\n---\n\n")), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_morning_recap",
			mcp.WithDescription("Morning recap: digest + decisions + action items across channels."),
			mcp.WithString("channels", mcp.Description("Comma-separated channel names. If empty, uses SLACK_CHANNELS.")),
			mcp.WithNumber("hours", mcp.Description("How many hours back (default: 24)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channelsStr := req.GetString("channels", "")
			hours := req.GetFloat("hours", 24)

			channelList := parseChannelList(channelsStr, client.Config().Channels)
			if len(channelList) == 0 {
				return mcp.NewToolResultError("No channels specified. Pass channels or set SLACK_CHANNELS env var."), nil
			}

			oldest := time.Now().Add(-time.Duration(int(hours)) * time.Hour)
			var allDecisions []string
			var allDigests []string

			for _, chName := range channelList {
				channelID, err := client.ResolveChannelID(chName)
				if err != nil {
					allDigests = append(allDigests, fmt.Sprintf("## #%s\nError: %v", chName, err))
					continue
				}

				messages, err := client.GetChannelHistory(channelID, oldest, 200)
				if err != nil {
					allDigests = append(allDigests, fmt.Sprintf("## #%s\nError: %v", chName, err))
					continue
				}

				userNames := resolveUsers(client, messages)
				allDigests = append(allDigests, slk.FormatDigest(chName, messages, userNames))

				decisions := detectDecisions(client, chName, messages, userNames)
				allDecisions = append(allDecisions, decisions...)
			}

			var b strings.Builder
			b.WriteString("# Morning Recap\n\n")

			if len(allDecisions) > 0 {
				b.WriteString("## Decisions & Approvals\n\n")
				for _, d := range allDecisions {
					b.WriteString(d)
					b.WriteString("\n")
				}
				b.WriteString("\n")
			}

			b.WriteString("## Channel Activity\n\n")
			b.WriteString(strings.Join(allDigests, "\n\n---\n\n"))

			return mcp.NewToolResultText(b.String()), nil
		},
	)
}

func channelDigest(client *slk.Client, channel string, hours int) (string, error) {
	channelID, err := client.ResolveChannelID(channel)
	if err != nil {
		return "", err
	}

	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)
	messages, err := client.GetChannelHistory(channelID, oldest, 200)
	if err != nil {
		return "", err
	}

	userNames := resolveUsers(client, messages)
	return slk.FormatDigest(channel, messages, userNames), nil
}

func parseChannelList(input string, defaults []string) []string {
	if input == "" {
		return defaults
	}
	var result []string
	for _, ch := range strings.Split(input, ",") {
		ch = strings.TrimSpace(strings.TrimPrefix(ch, "#"))
		if ch != "" {
			result = append(result, ch)
		}
	}
	return result
}

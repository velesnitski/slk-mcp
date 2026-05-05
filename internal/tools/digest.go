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

func registerDigestTools(s *server.MCPServer, d Deps) {
	if !d.Cfg.IsDisabled("get_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_channel_digest",
				mcp.WithDescription("Compact digest of recent messages from one channel. Either give a relative `hours` window (default), or absolute `after`/`before` (YYYY-MM-DD) for post-mortem-style fetches."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops)")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24). Ignored when after/before are set.")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages to inline (default: 50)")),
				mcp.WithString("after", mcp.Description("Absolute lower bound, YYYY-MM-DD (UTC). Overrides hours when set.")),
				mcp.WithString("before", mcp.Description("Absolute upper bound, YYYY-MM-DD (UTC, exclusive day end). Pair with after for date ranges.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				hours := int(req.GetFloat("hours", float64(d.Cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 50))
				after := req.GetString("after", "")
				before := req.GetString("before", "")

				oldest, latest, err := parseRange(after, before, hours)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				txt, err := channelDigestRange(ctx, d, channel, oldest, latest, maxShow)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(txt), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("get_multi_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_multi_channel_digest",
				mcp.WithDescription("Compact digest across multiple channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 20)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				list, _, err := resolveTargetChannels(ctx, d, req.GetString("channels", ""))
				if err != nil {
					return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
				}
				if len(list) == 0 {
					return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
				}
				hours := int(req.GetFloat("hours", float64(d.Cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 20))

				var parts []string
				for _, ch := range list {
					txt, err := channelDigest(ctx, d, ch, hours, maxShow)
					if err != nil {
						parts = append(parts, fmt.Sprintf("## #%s\nerror: %v", ch, err))
						continue
					}
					parts = append(parts, txt)
				}
				return mcp.NewToolResultText(strings.Join(parts, "\n\n")), nil
			},
		)
	}

	if !d.Cfg.IsDisabled("get_morning_recap") {
		s.AddTool(
			mcp.NewTool("get_morning_recap",
				mcp.WithDescription("Morning recap: decisions + channel activity across channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 15)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				list, _, err := resolveTargetChannels(ctx, d, req.GetString("channels", ""))
				if err != nil {
					return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
				}
				if len(list) == 0 {
					return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
				}
				hours := int(req.GetFloat("hours", float64(d.Cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 15))
				oldest := time.Now().Add(-time.Duration(hours) * time.Hour)

				var decisions []string
				var digests []string

				for _, ch := range list {
					channelID, err := d.Client.Channels.ResolveID(ctx, ch)
					if err != nil {
						digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
						continue
					}
					msgs, err := d.Client.Messages.History(ctx, slack.HistoryParams{
						ChannelID: channelID,
						OldestTS:  float64(oldest.Unix()),
						Limit:     d.Cfg.MaxMessagesPerChannel,
					})
					if err != nil {
						digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
						continue
					}
					users := d.Client.Users.NamesFor(ctx, collectUserIDs(msgs))
					digests = append(digests, format.ChannelDigest("#"+ch, msgs, users, maxShow))
					decisions = append(decisions, detectDecisions(d.Cfg, ch, msgs, users, format.DecisionLine)...)
				}

				var b strings.Builder
				fmt.Fprintf(&b, "# Morning Recap (last %dh)\n\n", hours)
				if len(decisions) > 0 {
					b.WriteString("## Decisions\n")
					for _, line := range decisions {
						b.WriteString(line)
						b.WriteByte('\n')
					}
					b.WriteByte('\n')
				}
				b.WriteString("## Activity\n")
				b.WriteString(strings.Join(digests, "\n\n"))
				return mcp.NewToolResultText(b.String()), nil
			},
		)
	}
}

func channelDigest(ctx context.Context, d Deps, channel string, hours, maxShow int) (string, error) {
	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)
	return channelDigestRange(ctx, d, channel, oldest, time.Time{}, maxShow)
}

func channelDigestRange(ctx context.Context, d Deps, channel string, oldest, latest time.Time, maxShow int) (string, error) {
	channelID, err := d.Client.Channels.ResolveID(ctx, channel)
	if err != nil {
		return "", err
	}
	p := slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  float64(oldest.Unix()),
		Limit:     d.Cfg.MaxMessagesPerChannel,
	}
	if !latest.IsZero() {
		p.LatestTS = float64(latest.Unix())
	}
	msgs, err := d.Client.Messages.History(ctx, p)
	if err != nil {
		return "", err
	}
	users := d.Client.Users.NamesFor(ctx, collectUserIDs(msgs))
	return format.ChannelDigest("#"+channel, msgs, users, maxShow), nil
}

// parseRange resolves the user's window into (oldest, latest). When
// after/before are set, they override hours. before is exclusive at
// end-of-day so "after=2026-04-30 before=2026-05-01" returns one full
// UTC day.
func parseRange(after, before string, hours int) (time.Time, time.Time, error) {
	if after == "" && before == "" {
		return time.Now().Add(-time.Duration(hours) * time.Hour), time.Time{}, nil
	}
	var oldest, latest time.Time
	if after != "" {
		t, err := time.Parse("2006-01-02", after)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("after must be YYYY-MM-DD: %w", err)
		}
		oldest = t
	}
	if before != "" {
		t, err := time.Parse("2006-01-02", before)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("before must be YYYY-MM-DD: %w", err)
		}
		latest = t.Add(24 * time.Hour)
	}
	if !oldest.IsZero() && !latest.IsZero() && !latest.After(oldest) {
		return time.Time{}, time.Time{}, fmt.Errorf("before must be after after")
	}
	return oldest, latest, nil
}

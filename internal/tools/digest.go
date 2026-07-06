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

func (h *Hub) registerDigestTools(s *server.MCPServer) {
	if !h.cfg.IsDisabled("get_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_channel_digest",
				mcp.WithDescription("Compact digest of recent messages from one channel. Either give a relative `hours` window (default), or absolute `after`/`before` (YYYY-MM-DD) for post-mortem-style fetches."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops)")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24). Ignored when after/before are set.")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages to inline (default: 50)")),
				mcp.WithString("after", mcp.Description("Absolute lower bound, YYYY-MM-DD (UTC). Overrides hours when set.")),
				mcp.WithString("before", mcp.Description("Absolute upper bound, YYYY-MM-DD (UTC, exclusive day end). Pair with after for date ranges.")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
				mcp.WithBoolean("full_text", mcp.Description("Render message bodies in full instead of truncating long ones to a compact preview (default: false). Use when ingesting a channel verbatim — e.g. into a knowledge base.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				scoped, _, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}
				hours := int(req.GetFloat("hours", float64(h.cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 50))
				after := req.GetString("after", "")
				before := req.GetString("before", "")
				fullText := req.GetBool("full_text", false)

				oldest, latest, err := parseRange(after, before, hours)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				txt, err := scoped.channelDigestRange(ctx, channel, oldest, latest, maxShow, fullText)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(txt), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_multi_channel_digest") {
		s.AddTool(
			mcp.NewTool("get_multi_channel_digest",
				mcp.WithDescription("Compact digest across multiple channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 20)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				list, _, err := h.resolveTargetChannels(ctx, req.GetString("channels", ""))
				if err != nil {
					return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
				}
				if len(list) == 0 {
					return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
				}
				hours := int(req.GetFloat("hours", float64(h.cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 20))

				var parts []string
				for _, ch := range list {
					txt, err := h.channelDigest(ctx, ch, hours, maxShow, false)
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

	if !h.cfg.IsDisabled("get_morning_recap") {
		s.AddTool(
			mcp.NewTool("get_morning_recap",
				mcp.WithDescription("Morning recap: decisions + channel activity across channels. Falls back to SLACK_CHANNELS."),
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window in hours (default: SLACK_DIGEST_HOURS or 24)")),
				mcp.WithNumber("max_messages", mcp.Description("Max messages per channel (default: 15)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				list, _, err := h.resolveTargetChannels(ctx, req.GetString("channels", ""))
				if err != nil {
					return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
				}
				if len(list) == 0 {
					return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
				}
				hours := int(req.GetFloat("hours", float64(h.cfg.DigestHours)))
				maxShow := int(req.GetFloat("max_messages", 15))
				oldest := time.Now().Add(-time.Duration(hours) * time.Hour)

				var decisions []string
				var digests []string

				for _, ch := range list {
					channelID, err := h.Channels().ResolveID(ctx, ch)
					if err != nil {
						digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
						continue
					}
					msgs, err := h.Messages().History(ctx, slack.HistoryParams{
						ChannelID: channelID,
						OldestTS:  float64(oldest.Unix()),
						Limit:     h.cfg.MaxMessagesPerChannel,
					})
					if err != nil {
						digests = append(digests, fmt.Sprintf("## #%s\nerror: %v", ch, err))
						continue
					}
					users := h.resolveRefs(ctx, msgs)
					digests = append(digests, format.ChannelDigest("#"+ch, msgs, users, maxShow))
					decisions = append(decisions, detectDecisions(h.cfg, ch, msgs, users, format.DecisionLine)...)
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

func (h *Hub) channelDigest(ctx context.Context, channel string, hours, maxShow int, fullText bool) (string, error) {
	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)
	return h.channelDigestRange(ctx, channel, oldest, time.Time{}, maxShow, fullText)
}

func (h *Hub) channelDigestRange(ctx context.Context, channel string, oldest, latest time.Time, maxShow int, fullText bool) (string, error) {
	channelID, err := h.Channels().ResolveID(ctx, channel)
	if err != nil {
		return "", err
	}
	p := slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  float64(oldest.Unix()),
		Limit:     h.cfg.MaxMessagesPerChannel,
	}
	if !latest.IsZero() {
		p.LatestTS = float64(latest.Unix())
	}
	msgs, err := h.Messages().History(ctx, p)
	if err != nil {
		return "", err
	}
	users := h.resolveRefs(ctx, msgs)
	var opts []format.DigestOption
	if fullText {
		opts = append(opts, format.WithFullText())
	}
	return format.ChannelDigest("#"+channel, msgs, users, maxShow, opts...), nil
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

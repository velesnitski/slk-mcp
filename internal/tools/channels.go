package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// filterUnjoined returns only channels the operator is NOT a member of.
// When the filter is off, the input slice is returned untouched so the
// happy path stays allocation-free.
func filterUnjoined(channels []goslack.Channel, unjoinedOnly bool) []goslack.Channel {
	if !unjoinedOnly {
		return channels
	}
	out := channels[:0]
	for _, ch := range channels {
		if !ch.IsMember {
			out = append(out, ch)
		}
	}
	return out
}

// renderChannelLine formats one entry of the list_channels output.
// Layout: `- #name[🔒] (member_count) [NOT JOINED] context`
// - 🔒 appears for private channels (a member-audit signal: which
//   private rooms are you in vs not).
// - [NOT JOINED] appears only when IsMember is false. Joined channels
//   stay quiet — the marker is loud-on-anomaly, silent-on-common-case.
// - Context falls back from topic → purpose so a channel with no
//   topic but a real purpose still carries a description.
func renderChannelLine(ch goslack.Channel) string {
	var b strings.Builder
	b.WriteString("- #")
	b.WriteString(ch.Name)
	if ch.IsPrivate {
		b.WriteString(" 🔒")
	}
	fmt.Fprintf(&b, " (%d)", ch.NumMembers)
	if !ch.IsMember {
		b.WriteString(" [NOT JOINED]")
	}
	context := strings.TrimSpace(ch.Topic.Value)
	if context == "" {
		context = strings.TrimSpace(ch.Purpose.Value)
	}
	if len(context) > 80 {
		context = context[:80] + "..."
	}
	if context != "" {
		b.WriteByte(' ')
		b.WriteString(context)
	}
	return b.String()
}

func (h *Hub) registerChannelTools(s *server.MCPServer) {
	if !h.cfg.IsDisabled("list_channels") {
		s.AddTool(
			mcp.NewTool("list_channels",
				mcp.WithDescription("List Slack channels the operator can see, ordered by member count. "+
					"Each entry marks [NOT JOINED] for channels the operator isn't a member of, "+
					"and 🔒 for private channels, so callers can audit membership. "+
					"Falls back from topic to purpose when topic is empty. "+
					"Pass unjoined_only=true to filter to channels the operator hasn't joined "+
					"(typical channel-audit use case)."),
				mcp.WithNumber("limit", mcp.Description("Max channels to return (default: 100)")),
				mcp.WithBoolean("unjoined_only", mcp.Description("If true, return only channels the operator is NOT a member of. Surfaces public channels you haven't joined yet — primary use case is workspace audit. Default: false.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				limit := int(req.GetFloat("limit", 100))
				unjoinedOnly := req.GetBool("unjoined_only", false)
				channels, err := h.Channels().List(ctx, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("list channels: %v", err)), nil
				}
				channels = filterUnjoined(channels, unjoinedOnly)
				sort.Slice(channels, func(i, j int) bool {
					return channels[i].NumMembers > channels[j].NumMembers
				})

				var b strings.Builder
				header := fmt.Sprintf("%d channels", len(channels))
				if unjoinedOnly {
					header += " (operator is not a member)"
				}
				b.WriteString(header)
				b.WriteByte('\n')
				for _, ch := range channels {
					b.WriteString(renderChannelLine(ch))
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_channel_info") {
		s.AddTool(
			mcp.NewTool("get_channel_info",
				mcp.WithDescription("Get a channel's topic, purpose, member count and created date. Optionally lists member display names. Accepts either a channel name (#devops, devops) or a Slack channel ID (C0ABC1234DE) — useful for resolving `<#CID>` references from message bodies."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops, devops) or Slack channel ID (C0ABC1234DE)")),
				mcp.WithBoolean("include_members", mcp.Description("Resolve and list channel members (default: false)")),
				mcp.WithNumber("members_limit", mcp.Description("Cap on members listed (default: 50, 0 = all)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				includeMembers := req.GetBool("include_members", false)
				membersLimit := int(req.GetFloat("members_limit", 50))

				// Skip name→id lookup when the caller already has the ID
				// (typical when chasing `<#CID>` references from a digest).
				trimmed := strings.TrimPrefix(input, "#")
				var channelID string
				if slack.IsChannelID(trimmed) {
					channelID = trimmed
				} else {
					channelID, err = h.Channels().ResolveID(ctx, input)
					if err != nil {
						return mcp.NewToolResultError(err.Error()), nil
					}
				}
				ch, err := h.Channels().Info(ctx, channelID)
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
					ids, err := h.Channels().Members(ctx, channelID, membersLimit)
					if err != nil {
						fmt.Fprintf(&b, "\nmembers_error: %s", err.Error())
					} else {
						names := h.Users().NamesFor(ctx, ids)
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

	// archive / unarchive are write operations; gated by SLACK_READ_ONLY
	// alongside post_message and add_reaction. Both go through the same
	// channel-resolution path so a caller can pass either a name or a
	// canonical channel ID (e.g. straight from list_channels output).
	if !h.cfg.ReadOnly && !h.cfg.IsDisabled("archive_channel") {
		s.AddTool(
			mcp.NewTool("archive_channel",
				mcp.WithDescription("Archive a Slack channel via conversations.archive. Reversible via unarchive_channel — Slack hides the channel from active lists and rejects new messages, but does not permanently delete it. Requires channels:manage (public) or groups:write (private) on the user token."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#general, general) or Slack channel ID (C0ABC1234DE)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				channelID, err := h.Channels().ResolveID(ctx, input)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := h.Channels().Archive(ctx, channelID); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("archived #%s (%s) — reversible via unarchive_channel", strings.TrimPrefix(input, "#"), channelID)), nil
			},
		)
	}

	if !h.cfg.ReadOnly && !h.cfg.IsDisabled("unarchive_channel") {
		s.AddTool(
			mcp.NewTool("unarchive_channel",
				mcp.WithDescription("Restore an archived channel via conversations.unarchive. Reverses archive_channel."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name or Slack channel ID")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				channelID, err := h.Channels().ResolveID(ctx, input)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := h.Channels().Unarchive(ctx, channelID); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("unarchived #%s (%s)", strings.TrimPrefix(input, "#"), channelID)), nil
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

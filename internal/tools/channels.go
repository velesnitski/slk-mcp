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
	"github.com/velesnitski/slk-mcp/internal/format"
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
//   - 🔒 appears for private channels (a member-audit signal: which
//     private rooms are you in vs not).
//   - [NOT JOINED] appears only when IsMember is false. Joined channels
//     stay quiet — the marker is loud-on-anomaly, silent-on-common-case.
//   - Context falls back from topic → purpose so a channel with no
//     topic but a real purpose still carries a description.
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
					"(typical channel-audit use case). "+
					"With several workspaces configured, omitting `workspace` lists each in its own labelled section."),
				mcp.WithNumber("limit", mcp.Description("Max channels to return (default: 100)")),
				mcp.WithBoolean("unjoined_only", mcp.Description("If true, return only channels the operator is NOT a member of. Surfaces public channels you haven't joined yet — primary use case is workspace audit. Default: false.")),
				mcp.WithString("workspace", mcp.Description(workspaceArgAll)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runListChannels(ctx,
					req.GetString("workspace", ""),
					int(req.GetFloat("limit", 100)),
					req.GetBool("unjoined_only", false)), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_channel_info") {
		s.AddTool(
			mcp.NewTool("get_channel_info",
				mcp.WithDescription("Get a channel's or DM's topic, purpose, member count and created date, and — with a user token — how far you have read it: your read marker, the newest message and how many messages by others came after the marker. Optionally lists member display names. Accepts a channel name (#devops, devops), a DM as @handle or bare U… user id, or a C/G/D conversation id — useful for resolving `<#CID>` references from message bodies and for checking whether a DM is really unread."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops, devops), a DM as @handle or bare U… user id, or a C/G/D conversation id")),
				mcp.WithBoolean("read_state", mcp.Description("Report your read marker and unread count (one extra history read; needs a user token). Default: true.")),
				mcp.WithBoolean("include_members", mcp.Description("Resolve and list channel members (default: false)")),
				mcp.WithNumber("members_limit", mcp.Description("Cap on members listed (default: 50, 0 = all)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				scoped, wsName, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
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
					// @handle and U… open the DM; names resolve as before.
					channelID, err = scoped.resolveConversation(ctx, input)
					if err != nil {
						return mcp.NewToolResultError(err.Error()), nil
					}
				}
				ch, err := scoped.Channels().Info(ctx, channelID)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				created := time.Unix(int64(ch.Created), 0).Format("2006-01-02")
				var b strings.Builder
				label := "#" + ch.Name
				if slack.IsDirectMessage(*ch) {
					label = channelDisplayLabel(ctx, *ch, scoped.Users()) + " (DM)"
				}
				fmt.Fprintf(&b,
					"%s%s\nmembers: %d\ncreated: %s\ntopic: %s\npurpose: %s\narchived: %v",
					label, h.wsLabel(wsName), ch.NumMembers, created,
					firstLine(ch.Topic.Value), firstLine(ch.Purpose.Value), ch.IsArchived,
				)
				if req.GetBool("read_state", true) && scoped.client.HasUserToken() {
					b.WriteString("\n")
					b.WriteString(scoped.readStateLine(ctx, channelID))
				}

				if includeMembers {
					ids, err := scoped.Channels().Members(ctx, channelID, membersLimit)
					if err != nil {
						fmt.Fprintf(&b, "\nmembers_error: %s", err.Error())
					} else {
						names := scoped.Users().NamesFor(ctx, ids)
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
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				scoped, wsName, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}
				channelID, err := scoped.Channels().ResolveID(ctx, input)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := scoped.Channels().Archive(ctx, channelID); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("archived #%s%s (%s) — reversible via unarchive_channel", strings.TrimPrefix(input, "#"), h.wsLabel(wsName), channelID)), nil
			},
		)
	}

	if !h.cfg.ReadOnly && !h.cfg.IsDisabled("unarchive_channel") {
		s.AddTool(
			mcp.NewTool("unarchive_channel",
				mcp.WithDescription("Restore an archived channel via conversations.unarchive. Reverses archive_channel."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name or Slack channel ID")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				scoped, wsName, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}
				channelID, err := scoped.Channels().ResolveID(ctx, input)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := scoped.Channels().Unarchive(ctx, channelID); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("unarchived #%s%s (%s)", strings.TrimPrefix(input, "#"), h.wsLabel(wsName), channelID)), nil
			},
		)
	}
}

// runListChannels renders the channel list for one workspace (named) or
// every workspace (empty arg). With several workspaces and no filter, each
// is rendered in its own labelled section, mirroring the digest sweeps; a
// single workspace yields the flat list byte-for-byte as before. An
// unknown label is reported rather than silently returning the primary.
func (h *Hub) runListChannels(ctx context.Context, workspace string, limit int, unjoinedOnly bool) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	multi := len(targets) > 1

	var sections []string
	for _, ws := range targets {
		body, err := h.withClient(ws.Client).renderChannelList(ctx, limit, unjoinedOnly)
		switch {
		case err != nil && !multi:
			return mcp.NewToolResultError(fmt.Sprintf("list channels: %v", err))
		case err != nil:
			sections = append(sections, workspaceSection(ws.Name, "_error: "+err.Error()+"_"))
		case !multi:
			return mcp.NewToolResultText(body)
		default:
			sections = append(sections, workspaceSection(ws.Name, body))
		}
	}
	return mcp.NewToolResultText(strings.Join(sections, "\n\n"))
}

// renderChannelList fetches, filters, sorts and renders the channel list
// for the workspace this Hub is scoped to. The returned body carries no
// workspace heading so runListChannels can wrap it per workspace.
func (h *Hub) renderChannelList(ctx context.Context, limit int, unjoinedOnly bool) (string, error) {
	channels, err := h.Channels().List(ctx, limit)
	if err != nil {
		return "", err
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
	return strings.TrimRight(b.String(), "\n"), nil
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

// readStateLine renders the operator's read state of one conversation
// (ADR 129): where the marker is, the newest message, and whether that
// message is read. A failure is reported on the line, never as a guess.
func (h *Hub) readStateLine(ctx context.Context, channelID string) string {
	rs, err := h.Unread().ReadState(ctx, channelID)
	if err != nil {
		return "read state: unavailable — " + err.Error()
	}
	return formatReadState(rs, h.Users().Name(ctx, newestUser(rs)))
}

func newestUser(rs slack.ReadState) string {
	if rs.Newest == nil {
		return ""
	}
	return rs.Newest.User
}

// formatReadState renders a ReadState in one line. Pure.
func formatReadState(rs slack.ReadState, newestAuthor string) string {
	var b strings.Builder
	b.WriteString("read state: ")
	if rs.LastRead == "" {
		b.WriteString("no read marker from Slack for your token (not a member, or not tracked)")
	} else {
		b.WriteString("last read " + format.ClockLabel(format.ParseTS(rs.LastRead)))
	}
	if rs.Newest == nil {
		b.WriteString(" · no messages")
		return b.String()
	}
	newest := "newest " + format.ClockLabel(format.ParseTS(rs.Newest.Timestamp))
	if newestAuthor != "" {
		newest += " " + newestAuthor
	}
	b.WriteString(" · " + newest)
	switch {
	case rs.LastRead == "":
	case rs.Unread == 0:
		b.WriteString(" · read")
	case rs.Capped:
		fmt.Fprintf(&b, " · %d+ unread", rs.Unread)
	default:
		fmt.Fprintf(&b, " · %d unread", rs.Unread)
	}
	b.WriteString(" (thread replies are tracked separately)")
	return b.String()
}

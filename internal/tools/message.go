package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// registerMessageTools wires get_message: one message, verbatim.
//
// Every other read surface truncates by design — the sweep previews at
// 280 chars, digests carry a max_chars budget — because they render MANY
// messages and one wall of text must not evict the rest (ADR 026). That
// leaves a gap: no way to read a single long post in full. This tool is
// the complement, and BECAUSE it renders exactly one message it applies
// no truncation at all.
func (h *Hub) registerMessageTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("get_message") {
		return
	}
	s.AddTool(
		mcp.NewTool("get_message",
			mcp.WithDescription("Fetch ONE message verbatim — full text, no truncation — plus its metadata (author, time, edited flag, reactions with counts, attached files, thread position). Pass a permalink (preferred: the workspace is auto-detected from the link's host) or channel + ts. This is the drill-in for any '(+N chars)' preview in a digest or sweep."),
			mcp.WithString("permalink", mcp.Description("Slack permalink to the message. The host picks the workspace automatically; thread replies are resolved through their thread_ts.")),
			mcp.WithString("channel", mcp.Description("Channel name, @handle DM, or C.../D... id — only needed when no permalink is given")),
			mcp.WithString("ts", mcp.Description("Message timestamp (e.g. 1714000000.000123) — only needed when no permalink is given")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle+" Overrides the permalink-host auto-detection.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			permalink := strings.TrimSpace(req.GetString("permalink", ""))
			channel := req.GetString("channel", "")
			ts := req.GetString("ts", "")
			wsArg := req.GetString("workspace", "")

			var parsed *slack.ParsedPermalink
			if permalink != "" {
				p, err := slack.ParseSlackPermalink(permalink)
				if err != nil {
					return mcp.NewToolResultError("permalink could not be parsed: " + err.Error()), nil
				}
				parsed = p
			}
			if parsed == nil && (channel == "" || ts == "") {
				return mcp.NewToolResultError("pass a permalink, or channel + ts"), nil
			}

			scoped, wsName, note, errRes := h.routeWorkspace(ctx, wsArg, permalink)
			if errRes != nil {
				return errRes, nil
			}

			var channelID, threadTS string
			if parsed != nil {
				channelID, ts, threadTS = parsed.ChannelID, parsed.TS, parsed.ThreadTS
			} else {
				id, err := scoped.resolveConversation(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				channelID = id
			}

			msg, parent, err := scoped.fetchMessageWithParent(ctx, channelID, ts, threadTS)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			refs := scoped.resolveRefs(ctx, gatherForRefs(msg, parent))
			label := channelID
			if names := scoped.Channels().NamesForIDs(ctx, []string{channelID}); names[channelID] != "" {
				label = "#" + names[channelID]
			}
			body := renderFullMessage(*msg, parent, refs, wsName, label, note)
			return mcp.NewToolResultText(body), nil
		},
	)
}

// routeWorkspace picks the workspace for a message lookup. An explicit
// label always wins; otherwise the permalink's host is matched against
// each workspace's own URL (from the cached auth.test), so a pasted
// link lands in the right workspace without the caller knowing which
// one that is. No match falls back to the primary — the channel lookup
// will then fail loudly rather than silently reading the wrong place.
func (h *Hub) routeWorkspace(ctx context.Context, wsArg, permalink string) (scoped *Hub, wsName, note string, errRes *mcp.CallToolResult) {
	if wsArg != "" || permalink == "" {
		s, n, e := h.scopedWorkspace(wsArg)
		return s, n, "", e
	}
	host := permalinkHost(permalink)
	if host == "" {
		s, n, e := h.scopedWorkspace("")
		return s, n, "", e
	}
	for _, ws := range h.Workspaces() {
		cand := h.withClient(ws.Client)
		turl, err := cand.Unread().TeamURL(ctx)
		if err != nil || turl == "" {
			continue // bot-only workspace: cannot self-identify, skip
		}
		if hostsEqual(host, turl) {
			note := ""
			if len(h.Workspaces()) > 1 {
				note = "workspace auto-detected from permalink"
			}
			return cand, ws.Name, note, nil
		}
	}
	s, n, e := h.scopedWorkspace("")
	if e != nil {
		return nil, "", "", e
	}
	return s, n, fmt.Sprintf("no configured workspace matches host %q — tried the primary", host), nil
}

// fetchMessageWithParent fetches the target message and, when it is a
// thread reply, its parent for context. The permalink's thread_ts makes
// the reply case explicit, so the reply is looked up inside its own
// thread rather than guessed from channel history.
func (h *Hub) fetchMessageWithParent(ctx context.Context, channelID, ts, threadTS string) (*goslack.Message, *goslack.Message, error) {
	if threadTS != "" && threadTS != ts {
		replies, err := h.Messages().ThreadReplies(ctx, channelID, threadTS)
		if err != nil {
			return nil, nil, fmt.Errorf("thread lookup: %w", err)
		}
		var parent *goslack.Message
		for i := range replies {
			if replies[i].Timestamp == threadTS {
				parent = &replies[i]
			}
			if replies[i].Timestamp == ts {
				return &replies[i], parent, nil
			}
		}
		return nil, nil, fmt.Errorf("no reply at ts %s in thread %s", ts, threadTS)
	}
	msg, err := h.Messages().MessageAt(ctx, channelID, ts)
	if err != nil {
		return nil, nil, err
	}
	return msg, nil, nil
}

// gatherForRefs collects the messages whose mentions need resolving.
func gatherForRefs(msg, parent *goslack.Message) []goslack.Message {
	out := []goslack.Message{*msg}
	if parent != nil {
		out = append(out, *parent)
	}
	return out
}

// renderFullMessage renders one message verbatim with its metadata.
// Deliberately WITHOUT any length cap: this tool exists as the escape
// hatch from every other surface's truncation, so truncating here would
// recreate the very gap it closes. Pure.
func renderFullMessage(msg goslack.Message, parent *goslack.Message, refs map[string]string, wsName, channelLabel, note string) string {
	var b strings.Builder

	author := refs[msg.User]
	if author == "" {
		author = msg.User
	}
	if author == "" && msg.Username != "" {
		author = msg.Username // bot messages carry a username, not a user id
	}

	fmt.Fprintf(&b, "message in %s [%s]", channelLabel, wsName)
	if note != "" {
		fmt.Fprintf(&b, " (%s)", note)
	}
	b.WriteString("\n")

	when := format.ParseTS(msg.Timestamp)
	fmt.Fprintf(&b, "from: %s", author)
	if !when.IsZero() {
		fmt.Fprintf(&b, " at %s", when.Format("2006-01-02 15:04:05"))
	}
	if msg.Edited != nil {
		b.WriteString(" (edited)")
	}
	b.WriteString("\n")

	// Thread position: a reply shows its parent as one context line; a
	// parent that has replies says so, pointing at the tool that renders
	// them — this tool stays single-message on purpose.
	if parent != nil {
		pAuthor := refs[parent.User]
		if pAuthor == "" {
			pAuthor = parent.User
		}
		fmt.Fprintf(&b, "reply in thread of: [%s] %s\n", pAuthor, previewLine(format.RenderText(parent.Text, refs), 200))
	} else if msg.ReplyCount > 0 {
		fmt.Fprintf(&b, "thread parent: %d replies (use get_thread for the full thread)\n", msg.ReplyCount)
	}

	text := format.RenderText(msg.Text, refs)
	fmt.Fprintf(&b, "chars: %d\n\n%s\n", len([]rune(text)), text)

	if len(msg.Files) > 0 {
		b.WriteString("\nfiles:\n")
		for _, f := range msg.Files {
			name := f.Name
			if name == "" {
				name = f.ID
			}
			fmt.Fprintf(&b, "  - %s (%s, %d bytes)\n", name, f.Mimetype, f.Size)
		}
	}
	if len(msg.Reactions) > 0 {
		parts := make([]string, 0, len(msg.Reactions))
		for _, r := range msg.Reactions {
			parts = append(parts, fmt.Sprintf(":%s: ×%d", r.Name, r.Count))
		}
		b.WriteString("reactions: " + strings.Join(parts, "  ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// previewLine flattens a message to one line capped at n runes — used
// only for the PARENT context line, never for the target message. Pure.
func previewLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// permalinkHost extracts the lowercase host from a permalink. Pure.
func permalinkHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

// hostsEqual compares a permalink host with a workspace base URL from
// auth.test (e.g. "https://team.slack.com/"). Pure.
func hostsEqual(host, teamURL string) bool {
	u, err := url.Parse(strings.TrimSpace(teamURL))
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(host, u.Host)
}

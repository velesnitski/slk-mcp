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
)

// registerScheduledTools wires list_scheduled_messages — the operator's
// pending scheduled (queued-to-send) messages. A read, but scheduled
// messages are per-identity, so it needs a user token and registers only
// when one exists. Not gated on ReadOnly — it mutates nothing.
func (h *Hub) registerScheduledTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("list_scheduled_messages") {
		return
	}
	anyUserToken := false
	for _, ws := range h.registry {
		if ws.Client.HasUserToken() {
			anyUserToken = true
			break
		}
	}
	if !anyUserToken {
		return
	}
	s.AddTool(
		mcp.NewTool("list_scheduled_messages",
			mcp.WithDescription("List your pending scheduled Slack messages — the ones queued to send later, with their send time and target channel. Read-only. Scheduled messages are a property of YOU, so an empty workspace lists them across EVERY configured workspace. Requires a user token (xoxp-)."),
			mcp.WithString("workspace", mcp.Description("Target one workspace by its configured label. Default (empty): list across EVERY workspace.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runListScheduled(ctx, req.GetString("workspace", "")), nil
		},
	)
}

// runListScheduled fetches and renders scheduled messages across the
// requested workspaces (empty = all, you-global). Bot-only workspaces
// are skipped; a per-workspace fetch error is reported inline so one bad
// workspace doesn't sink the rest.
func (h *Hub) runListScheduled(ctx context.Context, workspace string) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}

	var sections []string
	served := 0
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		label := h.wsLabel(ws.Name)
		if !scoped.Scheduled().Enabled() {
			continue
		}
		served++
		msgs, err := scoped.Scheduled().List(ctx)
		if err != nil {
			sections = append(sections, fmt.Sprintf("error listing scheduled messages%s: %s", label, scheduledErrHint(err)))
			continue
		}
		if len(msgs) == 0 {
			sections = append(sections, fmt.Sprintf("no scheduled messages%s", label))
			continue
		}
		ids := make([]string, 0, len(msgs))
		for _, m := range msgs {
			ids = append(ids, m.Channel)
		}
		names := scoped.Channels().NamesForIDs(ctx, ids)
		body := strings.Join(renderScheduled(msgs, names), "\n")
		sections = append(sections, fmt.Sprintf("%d scheduled message(s)%s:\n%s", len(msgs), label, body))
	}
	if served == 0 {
		return mcp.NewToolResultError("no workspace has a user token; cannot list scheduled messages")
	}
	return mcp.NewToolResultText(strings.Join(sections, "\n\n"))
}

// renderScheduled sorts scheduled messages soonest-first and renders one
// line each: channel (name if resolved, else the raw id), local send
// time, and a truncated preview. Pure — unit-tested without a live
// fetch. `names` maps channel IDs to names (may be partial for DMs /
// private channels the token can't see).
func renderScheduled(msgs []goslack.ScheduledMessage, names map[string]string) []string {
	sorted := append([]goslack.ScheduledMessage(nil), msgs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].PostAt < sorted[j].PostAt })

	lines := make([]string, 0, len(sorted))
	for _, m := range sorted {
		ch := m.Channel
		if name := names[m.Channel]; name != "" {
			ch = "#" + name
		}
		when := time.Unix(int64(m.PostAt), 0).Format("2006-01-02 15:04")
		lines = append(lines, fmt.Sprintf("- %s · %s — %s", ch, when, previewText(m.Text)))
	}
	return lines
}

// previewText collapses newlines and truncates to 80 runes (rune-safe so
// Cyrillic isn't split mid-character). Empty text is labelled rather than
// rendered as a blank preview.
func previewText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "(no text)"
	}
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// scheduledErrHint annotates a scope/token failure with the likely fix.
// chat.scheduledMessages.list reads the same queue chat scheduling
// writes to, so the scope to add is the messaging one.
func scheduledErrHint(err error) string {
	e := err.Error()
	if strings.Contains(e, "missing_scope") || strings.Contains(e, "not_allowed_token_type") {
		return e + " — the user token may need the messaging scope used to schedule (chat:write); add it under OAuth & Permissions, reinstall the app, then retry"
	}
	return e
}

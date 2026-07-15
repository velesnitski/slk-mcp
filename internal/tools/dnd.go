package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerDNDTools wires set_dnd — pause (or resume) the authenticated
// user's notifications (Slack Do Not Disturb / snooze). Registered only
// when at least one workspace carries a user token (a bot token cannot
// snooze a human) and only when not read-only. Like status, DND is a
// property of YOU, so an empty workspace applies to every configured
// workspace at once.
func (h *Hub) registerDNDTools(s *server.MCPServer) {
	if h.cfg.ReadOnly {
		return
	}
	if h.cfg.IsDisabled("set_dnd") {
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
		mcp.NewTool("set_dnd",
			mcp.WithDescription("Pause (or resume) your Slack notifications — Do Not Disturb / snooze. Unlike a custom status, this actually SILENCES notifications. DND is a property of YOU, so an empty workspace pauses notifications on EVERY configured workspace at once. Pass minutes>0 to snooze for that long; minutes=0 or resume=true ends the snooze now. Requires a user token (xoxp-) with the dnd:write scope."),
			mcp.WithNumber("minutes", mcp.Description("How long to pause notifications, in minutes (>0). 0 (or resume=true) ends the snooze immediately.")),
			mcp.WithBoolean("resume", mcp.Description("true = end the current snooze now (resume notifications), ignoring minutes. Default false.")),
			mcp.WithString("workspace", mcp.Description("Target one workspace by its configured label. Default (empty): apply to EVERY workspace — DND is you-global.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runSetDND(ctx, req.GetString("workspace", ""), int(req.GetFloat("minutes", 0)), req.GetBool("resume", false)), nil
		},
	)
}

// runSetDND snoozes or resumes notifications across the requested
// workspaces (empty = all, you-global). minutes<=0 or resume ends the
// snooze; otherwise notifications are paused for `minutes`.
func (h *Hub) runSetDND(ctx context.Context, workspace string, minutes int, resume bool) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	end := resume || minutes <= 0

	var lines []string
	applied := 0
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		label := h.wsLabel(ws.Name)
		if !scoped.DND().Enabled() {
			lines = append(lines, fmt.Sprintf("- skipped%s: no user token for this workspace", label))
			continue
		}
		var err error
		if end {
			err = scoped.DND().EndSnooze(ctx)
		} else {
			err = scoped.DND().Snooze(ctx, minutes)
		}
		if err != nil {
			lines = append(lines, fmt.Sprintf("- error%s: %s", label, dndErrorHint(err)))
			continue
		}
		if end {
			lines = append(lines, fmt.Sprintf("- notifications resumed%s", label))
		} else {
			lines = append(lines, fmt.Sprintf("- notifications paused for %d min%s", minutes, label))
		}
		applied++
	}
	if applied == 0 {
		return mcp.NewToolResultError("no workspace has a user token; cannot change DND:\n" + strings.Join(lines, "\n"))
	}
	return mcp.NewToolResultText(strings.Join(lines, "\n"))
}

// dndErrorHint maps Slack's terse missing_scope on the dnd.* methods to
// an actionable message naming the scope to add. Mirrors statusErrorHint.
func dndErrorHint(err error) string {
	e := err.Error()
	if strings.Contains(e, "missing_scope") {
		return e + " — the user token lacks the dnd:write scope: add it under OAuth & Permissions → User Token Scopes, reinstall the app, then retry"
	}
	return e
}

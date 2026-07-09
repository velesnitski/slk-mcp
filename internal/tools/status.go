package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerStatusTools wires set_status and set_presence — the
// authenticated user's custom status + presence. Registered only when
// at least one workspace carries a user token (a bot token cannot set a
// human's status) and only when not read-only (they mutate the
// profile).
func (h *Hub) registerStatusTools(s *server.MCPServer) {
	if h.cfg.ReadOnly {
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

	h.registerSetPresence(s)

	if h.cfg.IsDisabled("set_status") {
		return
	}
	s.AddTool(
		mcp.NewTool("set_status",
			mcp.WithDescription("Set (or clear) your Slack custom status and, optionally, presence. Unlike posting, a status is a property of YOU — so an empty workspace sets it on EVERY configured workspace at once (you are away from all of them). Clear the status by passing empty text and emoji. Requires a user token (xoxp-)."),
			mcp.WithString("text", mcp.Description("Status text, e.g. 'AFK till tomorrow'. Empty text + empty emoji clears the status.")),
			mcp.WithString("emoji", mcp.Description("Status emoji in colon form, e.g. ':palm_tree:'. Optional; if text is set but emoji is empty, Slack shows the text with a default speech-balloon icon.")),
			mcp.WithNumber("clear_after_minutes", mcp.Description("Auto-clear the status after N minutes from now (server computes the expiry). 0 or omitted = no expiry. For 'AFK till next work day' pass the minutes until tomorrow morning.")),
			mcp.WithBoolean("away", mcp.Description("Also force presence to 'away' (the grey dot). Default false leaves presence automatic. Pass false explicitly to restore automatic presence when clearing an AFK status.")),
			mcp.WithBoolean("set_presence", mcp.Description("Whether to touch presence at all (default: false). When true, presence is set from `away`; when false, presence is left untouched. Lets you set a status without also flipping the dot.")),
			mcp.WithString("workspace", mcp.Description("Target one workspace by its configured label. Default (empty): apply to EVERY workspace — a status is you-global.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runSetStatus(ctx, statusParams{
				workspace:   req.GetString("workspace", ""),
				text:        req.GetString("text", ""),
				emoji:       normalizeEmoji(req.GetString("emoji", "")),
				clearAfter:  int(req.GetFloat("clear_after_minutes", 0)),
				away:        req.GetBool("away", false),
				setPresence: req.GetBool("set_presence", false),
			}, time.Now()), nil
		},
	)
}

// registerSetPresence wires set_presence — flip the away/auto dot
// WITHOUT touching the custom status. set_status can also set presence
// (for setting an AFK status + dot in one call), but that path clears
// the status when no text is given; set_presence is the standalone way
// to go away/back while keeping whatever status is already up.
func (h *Hub) registerSetPresence(s *server.MCPServer) {
	if h.cfg.IsDisabled("set_presence") {
		return
	}
	s.AddTool(
		mcp.NewTool("set_presence",
			mcp.WithDescription("Set your Slack presence to away (the grey dot) or auto, WITHOUT changing your custom status. Presence is a property of YOU, so an empty workspace applies it to every configured workspace. Use set_status when you want to change the status text too. Requires a user token (xoxp-)."),
			mcp.WithBoolean("away", mcp.Description("true (default) = force presence to 'away' (grey dot); false = restore automatic presence (Slack flips to away on idle again).")),
			mcp.WithString("workspace", mcp.Description("Target one workspace by its configured label. Default (empty): apply to EVERY workspace — presence is you-global.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runSetPresence(ctx, req.GetString("workspace", ""), req.GetBool("away", true)), nil
		},
	)
}

// runSetPresence flips presence across the requested workspaces (empty =
// all, you-global) and leaves the custom status untouched.
func (h *Hub) runSetPresence(ctx context.Context, workspace string, away bool) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	want := "auto"
	if away {
		want = "away"
	}

	var lines []string
	applied := 0
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		label := h.wsLabel(ws.Name)
		if !scoped.Status().Enabled() {
			lines = append(lines, fmt.Sprintf("- skipped%s: no user token for this workspace", label))
			continue
		}
		if err := scoped.Status().SetPresence(ctx, away); err != nil {
			lines = append(lines, fmt.Sprintf("- error%s: %s", label, statusErrorHint(err)))
			continue
		}
		lines = append(lines, fmt.Sprintf("- presence: %s%s", want, label))
		applied++
	}
	if applied == 0 {
		return mcp.NewToolResultError("no workspace has a user token; cannot set presence:\n" + strings.Join(lines, "\n"))
	}
	return mcp.NewToolResultText(strings.Join(lines, "\n"))
}

// statusParams bundles the parsed set_status arguments.
type statusParams struct {
	workspace   string
	text        string
	emoji       string
	clearAfter  int
	away        bool
	setPresence bool
}

// normalizeEmoji tolerates a bare emoji name ("palm_tree") and wraps it
// in the colons Slack requires (":palm_tree:"). An already-wrapped value
// or an empty string passes through untouched.
func normalizeEmoji(e string) string {
	e = strings.TrimSpace(e)
	if e == "" || strings.HasPrefix(e, ":") {
		return e
	}
	return ":" + strings.Trim(e, ":") + ":"
}

// statusErrorHint maps Slack's terse status-write failures to an
// actionable message. missing_scope is the one every fresh install
// hits: the user token needs users.profile:write (status) and
// users:write (presence), which the pre-1.2 manifest never asked for.
func statusErrorHint(err error) string {
	e := err.Error()
	if strings.Contains(e, "missing_scope") {
		return e + " — the user token lacks the profile scopes: add users.profile:write (status) and users:write (presence) under OAuth & Permissions → User Token Scopes, reinstall the app, then retry"
	}
	return e
}

// describeStatus renders a human-readable confirmation of what was set.
func describeStatus(text, emoji string) string {
	if text == "" && emoji == "" {
		return "cleared custom status"
	}
	desc := "set status"
	if emoji != "" {
		desc += " " + emoji
	}
	if text != "" {
		desc += fmt.Sprintf(" %q", text)
	}
	return desc
}

// statusExpiry turns a "clear after N minutes" duration into a Slack
// status_expiration Unix timestamp. 0 (or negative) minutes means no
// expiry — Slack's sentinel for a status that stays until cleared.
func statusExpiry(clearAfterMinutes int, now time.Time) int64 {
	if clearAfterMinutes <= 0 {
		return 0
	}
	return now.Add(time.Duration(clearAfterMinutes) * time.Minute).Unix()
}

// runSetStatus applies the status/presence change to the requested
// workspaces. Empty workspace means ALL (a status is you-global, the
// deliberate opposite of post_message's single-target default). `now`
// is injected so the expiry computation is unit-testable.
func (h *Hub) runSetStatus(ctx context.Context, p statusParams, now time.Time) *mcp.CallToolResult {
	targets := h.workspaceTargets(p.workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(p.workspace, h.workspaceNames()))
	}

	expiration := statusExpiry(p.clearAfter, now)
	multi := len(targets) > 1

	var lines []string
	applied := 0
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		label := h.wsLabel(ws.Name)
		if !scoped.Status().Enabled() {
			// A bot-only workspace can't carry a personal status; report
			// it rather than silently skipping so the operator knows the
			// AFK didn't land everywhere.
			lines = append(lines, fmt.Sprintf("- skipped%s: no user token for this workspace", label))
			continue
		}
		if err := scoped.Status().SetCustomStatus(ctx, p.text, p.emoji, expiration); err != nil {
			if !multi {
				return mcp.NewToolResultError(statusErrorHint(err))
			}
			lines = append(lines, fmt.Sprintf("- error%s: %s", label, statusErrorHint(err)))
			continue
		}
		line := "- " + describeStatus(p.text, p.emoji) + label
		if expiration > 0 {
			line += fmt.Sprintf(" (clears at %s)", time.Unix(expiration, 0).Format("2006-01-02 15:04"))
		}
		if p.setPresence {
			if err := scoped.Status().SetPresence(ctx, p.away); err != nil {
				line += fmt.Sprintf("; presence unchanged (%v)", err)
			} else if p.away {
				line += "; presence: away"
			} else {
				line += "; presence: auto"
			}
		}
		lines = append(lines, line)
		applied++
	}

	if applied == 0 {
		return mcp.NewToolResultError("no workspace has a user token; cannot set status:\n" + strings.Join(lines, "\n"))
	}
	return mcp.NewToolResultText(strings.Join(lines, "\n"))
}

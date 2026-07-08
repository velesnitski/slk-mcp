package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerStatusTools wires set_status — the authenticated user's
// custom status + presence. Registered only when at least one workspace
// carries a user token (a bot token cannot set a human's status) and
// only when not read-only (it mutates the profile).
func (h *Hub) registerStatusTools(s *server.MCPServer) {
	if h.cfg.ReadOnly || h.cfg.IsDisabled("set_status") {
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
				return mcp.NewToolResultError(err.Error())
			}
			lines = append(lines, fmt.Sprintf("- error%s: %v", label, err))
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

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Slack's own priorities – the VIP list and starred conversations – read
// from Slack and kept in a small local file per workspace, so every tool
// can use them without asking Slack each time. The file holds IDs only,
// never message text or tokens. Refreshed by sync_slack_prefs, and on its
// own when older than prefsMaxAge. See ADR 143.

// storedPrefs is the local file's content.
type storedPrefs struct {
	Workspace string    `json:"workspace"`
	FetchedAt time.Time `json:"fetched_at"`
	VIPs      []string  `json:"vips"`
	VIPKeys   []string  `json:"vip_keys,omitempty"`
	Starred   []string  `json:"starred"`
	Sources   []string  `json:"sources,omitempty"`
	Failures  []string  `json:"failures,omitempty"`
}

// prefsMaxAge is how old the local copy may be before a tool refreshes it;
// prefsRefreshTimeout bounds that refresh inside a tool call.
const (
	prefsMaxAge         = 24 * time.Hour
	prefsRefreshTimeout = 10 * time.Second
	prefsFailBackoff    = 30 * time.Minute
)

var (
	prefsMu       sync.Mutex
	prefsMem      = map[string]storedPrefs{} // path → content
	prefsFailedAt = map[string]time.Time{}   // path → last failed refresh
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// prefsPath is the file for this Hub's workspace, or "" when local state
// is off.
func (h *Hub) prefsPath(workspace string) string {
	if h.cfg.StateDir == "" {
		return ""
	}
	name := unsafeName.ReplaceAllString(strings.ToLower(workspace), "_")
	if name == "" {
		name = "primary"
	}
	return filepath.Join(h.cfg.StateDir, "slack-prefs-"+name+".json")
}

// slackPrefs returns the stored priorities for this workspace, refreshing
// them when missing or older than prefsMaxAge. A refresh that fails keeps
// the old copy and is not retried for prefsFailBackoff, so a workspace
// that refuses the undocumented endpoints costs one attempt, not one per
// call.
func (h *Hub) slackPrefs(ctx context.Context, workspace string) storedPrefs {
	path := h.prefsPath(workspace)
	if path == "" || !h.client.HasUserToken() {
		return storedPrefs{}
	}
	prefsMu.Lock()
	cur, ok := prefsMem[path]
	failedAt := prefsFailedAt[path]
	prefsMu.Unlock()
	if !ok {
		cur, ok = readPrefsFile(path)
	}
	if ok && time.Since(cur.FetchedAt) < prefsMaxAge {
		prefsMu.Lock()
		prefsMem[path] = cur
		prefsMu.Unlock()
		return cur
	}
	if time.Since(failedAt) < prefsFailBackoff {
		return cur
	}
	rctx, cancel := context.WithTimeout(ctx, prefsRefreshTimeout)
	defer cancel()
	fresh, err := h.syncPrefs(rctx, workspace)
	if err != nil {
		prefsMu.Lock()
		prefsFailedAt[path] = time.Now()
		prefsMu.Unlock()
		return cur
	}
	return fresh
}

// syncPrefs reads Slack now and stores the result.
func (h *Hub) syncPrefs(ctx context.Context, workspace string) (storedPrefs, error) {
	p, err := h.Canvas().FetchPrefs(ctx)
	if err != nil && !allRefused(p.Failures) {
		return storedPrefs{}, err
	}
	// A refusal Slack will repeat (a scope no app can be granted, a method
	// closed to user tokens) is an answer, not an outage: it is stored like
	// a result, so the next attempt is a day away rather than half an hour.
	sp := storedPrefs{
		Workspace: workspace, FetchedAt: time.Now().UTC(),
		VIPs: p.VIPs, VIPKeys: p.VIPKeys, Starred: p.Starred,
		Sources: p.Sources, Failures: p.Failures,
	}
	if path := h.prefsPath(workspace); path != "" {
		if err := writePrefsFile(path, sp); err != nil {
			return sp, err
		}
		prefsMu.Lock()
		prefsMem[path] = sp
		delete(prefsFailedAt, path)
		prefsMu.Unlock()
	}
	// The VIP set is derived from these; drop its cache.
	vipCacheMu.Lock()
	delete(vipCache, h.client)
	vipCacheMu.Unlock()
	return sp, nil
}

func readPrefsFile(path string) (storedPrefs, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return storedPrefs{}, false
	}
	var sp storedPrefs
	if json.Unmarshal(raw, &sp) != nil {
		return storedPrefs{}, false
	}
	return sp, true
}

// writePrefsFile writes atomically (temp file + rename) with owner-only
// permissions: the file names the people the operator watches most.
func writePrefsFile(path string, sp storedPrefs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".slack-prefs-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// starredSet is the starred conversations of this workspace: the ones
// read from Slack plus SLACK_STARRED_CHANNELS, resolved here (names or
// IDs; a name this workspace does not have is skipped).
func (h *Hub) starredSet(ctx context.Context, workspace string) map[string]bool {
	out := map[string]bool{}
	for _, id := range h.slackPrefs(ctx, workspace).Starred {
		out[id] = true
	}
	for _, ref := range h.cfg.StarredChannels {
		if id, err := h.Channels().ResolveID(ctx, ref); err == nil {
			out[id] = true
		}
	}
	return out
}

// permanentRefusal reports a Slack answer that will not change by
// retrying: the method needs a scope no current app can be granted
// ("read", "stars:read"), or is closed to user tokens.
func permanentRefusal(failure string) bool {
	for _, s := range []string{"missing_scope", "not_allowed_token_type", "unknown_method", "method_deprecated", "invalid_auth_type"} {
		if strings.Contains(failure, s) {
			return true
		}
	}
	return false
}

// allRefused reports whether every failed source refused for good.
func allRefused(failures []string) bool {
	if len(failures) == 0 {
		return false
	}
	for _, f := range failures {
		if !permanentRefusal(f) {
			return false
		}
	}
	return true
}

// explainFailure turns a refused source into what it means for the
// operator. The scopes Slack names in these errors ("read" for the
// preferences, "stars:read" for stars) are legacy: an app created today
// cannot be granted them, so asking for them would send the operator
// looking for options that are not in the list.
func explainFailure(f string) string {
	method, _, _ := strings.Cut(f, ":")
	switch {
	case method == "users.prefs.get" && permanentRefusal(f):
		return "Slack's VIP list: not available to apps (Slack's own preferences need a legacy scope it no longer grants)"
	case method == "stars.list" && permanentRefusal(f):
		return "stars: not available (Slack retired stars for Saved items, with their scope)"
	case method == "users.channelSections.list" && permanentRefusal(f):
		return "sidebar Starred section: closed to app tokens"
	}
	return f
}

// registerPrefsTools wires sync_slack_prefs.
func (h *Hub) registerPrefsTools(s *server.MCPServer) {
	h.register(s, toolDef{
		Name:        "sync_slack_prefs",
		Description: "Read your Slack VIP list (people whose messages ring through Do Not Disturb) and your starred conversations from Slack, and keep them locally so get_unread_summary and get_my_actions put them first. Runs on its own once a day; call it after changing VIPs or stars in Slack. Uses Slack's client endpoints, which are not part of the documented API and are usually refused to apps (they need legacy scopes Slack no longer grants): each source is reported, and any that refuses is skipped. Stores only IDs. SLACK_VIP_USERS and SLACK_STARRED_CHANNELS set the same priorities by hand and always apply.",
		Opts: []mcp.ToolOption{
			mcp.WithString("workspace", mcp.Description(workspaceArgAll)),
		},
		RequiresUserToken: true,
		Handle: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runSyncPrefs(ctx, req.GetString("workspace", "")), nil
		},
	})
}

func (h *Hub) runSyncPrefs(ctx context.Context, workspace string) *mcp.CallToolResult {
	targets, _ := h.sweepTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	if h.cfg.StateDir == "" {
		return mcp.NewToolResultError("local state is off (SLACK_STATE_DIR=off): nothing to sync into")
	}
	var out []string
	ok := 0
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		label := h.wsLabel(ws.Name)
		if !scoped.client.HasUserToken() {
			out = append(out, fmt.Sprintf("- skipped%s: no user token", label))
			continue
		}
		sp, err := scoped.syncPrefs(ctx, ws.Name)
		if err != nil {
			out = append(out, fmt.Sprintf("- error%s: %s", label, err))
			continue
		}
		if len(sp.Sources) == 0 {
			// Every source refused for good: report it plainly, not as an error.
			lines := []string{fmt.Sprintf("- nothing readable from Slack%s", label)}
			for _, f := range sp.Failures {
				lines = append(lines, "  "+explainFailure(f))
			}
			lines = append(lines, "  set them instead: SLACK_VIP_USERS (people) and SLACK_STARRED_CHANNELS (channels)")
			out = append(out, strings.Join(lines, "\n"))
			ok++
			continue
		}
		ok++
		names := scoped.Users().NamesFor(ctx, sp.VIPs)
		chans := scoped.Channels().NamesForIDs(ctx, sp.Starred)
		vipNames := make([]string, 0, len(sp.VIPs))
		for _, id := range sp.VIPs {
			vipNames = append(vipNames, firstNonEmpty(names[id], id))
		}
		starNames := make([]string, 0, len(sp.Starred))
		for _, id := range sp.Starred {
			n := firstNonEmpty(chans[id], id)
			if strings.HasPrefix(id, "D") {
				n = "DM " + n
			} else if !strings.HasPrefix(n, "#") && !strings.HasPrefix(n, "DM") {
				n = "#" + n
			}
			starNames = append(starNames, n)
		}
		line := fmt.Sprintf("- synced%s: %d VIP, %d starred from Slack", label, len(sp.VIPs), len(sp.Starred))
		if len(vipNames) > 0 {
			line += "\n  VIP: " + strings.Join(vipNames, ", ")
		}
		if len(starNames) > 0 {
			line += "\n  starred: " + strings.Join(starNames, ", ")
		}
		for _, f := range sp.Failures {
			line += "\n  " + explainFailure(f)
		}
		if len(sp.VIPs) == 0 || len(sp.Starred) == 0 {
			line += "\n  set them instead: SLACK_VIP_USERS (people) and SLACK_STARRED_CHANNELS (channels)"
		}
		out = append(out, line)
	}
	text := strings.Join(out, "\n")
	if ok == 0 {
		return mcp.NewToolResultError(text)
	}
	return mcp.NewToolResultText(text + "\nstored in " + h.cfg.StateDir)
}

// workspaceName is the label of the workspace this Hub is scoped to.
func (h *Hub) workspaceName() string {
	for _, ws := range h.registry {
		if ws.Client == h.client {
			return ws.Name
		}
	}
	return "primary"
}

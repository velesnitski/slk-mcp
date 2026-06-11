package tools

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// twoWorkspaceHub builds a Hub serving two workspaces without any network
// — slack.New only constructs clients, it issues no API calls.
func twoWorkspaceHub(t *testing.T) *Hub {
	t.Helper()
	log := testLog()
	reg := []slack.Workspace{
		{Name: "primary", Client: slack.New(&config.Config{UserToken: "xoxp-a"}, log)},
		{Name: "secondary", Client: slack.New(&config.Config{UserToken: "xoxp-b"}, log)},
	}
	return NewHubWithRegistry(reg, &config.Config{}, log)
}

func TestNewHub_SingleWorkspaceRegistry(t *testing.T) {
	log := testLog()
	cfg := &config.Config{UserToken: "xoxp-a"}
	hub := NewHub(slack.New(cfg, log), cfg, log)
	if got := len(hub.Workspaces()); got != 1 {
		t.Fatalf("NewHub should yield a 1-element registry, got %d", got)
	}
	if hub.multiWorkspace() {
		t.Fatal("single workspace should not report multiWorkspace")
	}
	if name := hub.Workspaces()[0].Name; name != "primary" {
		t.Fatalf("default label should be primary, got %q", name)
	}
}

func TestNewHub_NamesPrimaryFromConfig(t *testing.T) {
	log := testLog()
	cfg := &config.Config{
		UserToken:  "xoxp-a",
		Workspaces: []config.WorkspaceConfig{{Name: "main", UserToken: "xoxp-a"}},
	}
	hub := NewHub(slack.New(cfg, log), cfg, log)
	if name := hub.Workspaces()[0].Name; name != "main" {
		t.Fatalf("primary label should come from config, got %q", name)
	}
}

func TestNewHubWithRegistry_PrimaryIsClientZero(t *testing.T) {
	hub := twoWorkspaceHub(t)
	if !hub.multiWorkspace() {
		t.Fatal("two workspaces should report multiWorkspace")
	}
	if hub.client != hub.Workspaces()[0].Client {
		t.Fatal("h.client must be registry[0] (primary)")
	}
}

func TestNewHubWithRegistry_PanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty registry")
		}
	}()
	NewHubWithRegistry(nil, &config.Config{}, testLog())
}

func TestWorkspaceTargets(t *testing.T) {
	hub := twoWorkspaceHub(t)

	if got := hub.workspaceTargets(""); len(got) != 2 {
		t.Fatalf("empty arg should target all workspaces, got %d", len(got))
	}
	if got := hub.workspaceTargets("secondary"); len(got) != 1 || got[0].Name != "secondary" {
		t.Fatalf("named arg should scope to one workspace, got %+v", got)
	}
	if got := hub.workspaceTargets("SeCoNdArY"); len(got) != 1 {
		t.Fatal("workspace match should be case-insensitive")
	}
	if got := hub.workspaceTargets("nope"); got != nil {
		t.Fatalf("unknown workspace should return nil, got %+v", got)
	}
}

func TestWithClient_RetargetsClientAndConfig(t *testing.T) {
	hub := twoWorkspaceHub(t)
	secondary := hub.Workspaces()[1].Client
	scoped := hub.withClient(secondary)

	if scoped.client != secondary {
		t.Fatal("withClient should swap the client")
	}
	if scoped.cfg != secondary.Config() {
		t.Fatal("withClient should swap cfg to the client's own config")
	}
	// Parent is untouched (no shared mutation).
	if hub.client == secondary {
		t.Fatal("withClient must not mutate the parent hub")
	}
}

func TestRunUnreadSummary_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	// workspaceTargets returns nil before any API call, so this exercises
	// the error path with no network.
	res := hub.runUnreadSummary(context.Background(), unreadParams{}, "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestRunMentions_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runMentions(context.Background(), mentionParams{}, "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestWorkspaceHelpers_PureFormatting(t *testing.T) {
	if got := workspaceSection("primary", "body"); got != "## [primary]\nbody" {
		t.Fatalf("workspaceSection: %q", got)
	}
	if got := unreadEmptyMsg(true); got != "no unread channels mention you" {
		t.Fatalf("unreadEmptyMsg(mentionsOnly): %q", got)
	}
	if got := unreadEmptyMsg(false); got != "all caught up — 0 unread" {
		t.Fatalf("unreadEmptyMsg: %q", got)
	}
	if got := mentionsEmptyMsg(false, 72); got != "no mentions in last 72h" {
		t.Fatalf("mentionsEmptyMsg: %q", got)
	}
}
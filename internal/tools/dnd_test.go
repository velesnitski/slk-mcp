package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func TestDNDErrorHint(t *testing.T) {
	if got := dndErrorHint(errors.New("missing_scope")); !strings.Contains(got, "dnd:write") {
		t.Fatalf("missing_scope should carry the dnd:write fix, got %q", got)
	}
	if got := dndErrorHint(errors.New("ratelimited")); got != "ratelimited" {
		t.Fatalf("other errors pass through, got %q", got)
	}
}

func TestRunSetDND_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runSetDND(context.Background(), "ghost", 30, false)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestRunSetDND_NoUserTokenIsError(t *testing.T) {
	// A workspace with no user token can't snooze; runSetDND must report
	// it rather than pretending it worked. The empty config yields a
	// client whose DND service is disabled, so no network call is made.
	reg := []slack.Workspace{
		{Name: "primary", Client: slack.New(&config.Config{}, testLog())},
	}
	hub := NewHubWithRegistry(reg, &config.Config{}, testLog())
	res := hub.runSetDND(context.Background(), "", 30, false)
	if res == nil || !res.IsError {
		t.Fatalf("no user token should error, got %+v", res)
	}
}

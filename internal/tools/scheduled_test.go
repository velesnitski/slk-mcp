package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func TestRenderScheduled_SortsSoonestFirstAndResolvesNames(t *testing.T) {
	msgs := []goslack.ScheduledMessage{
		{ID: "Q2", Channel: "C_GEN", PostAt: 2000, Text: "later one"},
		{ID: "Q1", Channel: "C_DEV", PostAt: 1000, Text: "sooner one"},
		{ID: "Q3", Channel: "D_UNKNOWN", PostAt: 3000, Text: "dm one"},
	}
	names := map[string]string{"C_GEN": "general", "C_DEV": "dev-backend"} // D_UNKNOWN unresolved
	lines := renderScheduled(msgs, names)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	// Soonest (PostAt 1000) must be first, latest (3000) last.
	if !strings.Contains(lines[0], "sooner one") || !strings.Contains(lines[0], "#dev-backend") {
		t.Errorf("line 0 should be the soonest #dev-backend message; got %q", lines[0])
	}
	if !strings.Contains(lines[2], "dm one") {
		t.Errorf("line 2 should be the latest message; got %q", lines[2])
	}
	// An unresolved channel id renders raw (no leading '#').
	if strings.Contains(lines[2], "#D_UNKNOWN") || !strings.Contains(lines[2], "D_UNKNOWN") {
		t.Errorf("unresolved channel should render as the raw id; got %q", lines[2])
	}
}

func TestPreviewText(t *testing.T) {
	if got := previewText(""); got != "(no text)" {
		t.Errorf("empty text should be labelled; got %q", got)
	}
	if got := previewText("line one\nline two"); strings.Contains(got, "\n") {
		t.Errorf("newlines should be collapsed; got %q", got)
	}
	// Rune-safe truncation: 100 Cyrillic chars must not split a rune and
	// must carry the ellipsis.
	long := strings.Repeat("я", 100)
	got := previewText(long)
	if r := []rune(got); len(r) != 81 || string(r[len(r)-1]) != "…" {
		t.Errorf("expected 80 runes + ellipsis, got %d runes: %q", len(r), got)
	}
}

func TestScheduledErrHint(t *testing.T) {
	if got := scheduledErrHint(errors.New("not_allowed_token_type")); !strings.Contains(got, "chat:write") {
		t.Fatalf("token/scope errors should carry the fix, got %q", got)
	}
	if got := scheduledErrHint(errors.New("ratelimited")); got != "ratelimited" {
		t.Fatalf("other errors pass through, got %q", got)
	}
}

func TestRunListScheduled_NoUserTokenIsError(t *testing.T) {
	// A workspace with no user token can't list scheduled messages;
	// runListScheduled reports it rather than returning an empty result.
	reg := []slack.Workspace{
		{Name: "primary", Client: slack.New(&config.Config{}, testLog())},
	}
	hub := NewHubWithRegistry(reg, &config.Config{}, testLog())
	res := hub.runListScheduled(context.Background(), "")
	if res == nil || !res.IsError {
		t.Fatalf("no user token should error, got %+v", res)
	}
}

func TestRunListScheduled_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runListScheduled(context.Background(), "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestScheduledEmptyMsg_CarriesTheAPIOnlyCaveat(t *testing.T) {
	got := scheduledEmptyMsg(" [primary]")
	// The label must survive, and the message must NOT read as a bare
	// "you have none" — a UI-scheduled queue is invisible to this API.
	for _, want := range []string{"[primary]", "VIA THE API", "Slack UI", "Drafts & sent"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty message missing %q; got: %s", want, got)
		}
	}
	if strings.HasPrefix(got, "no scheduled messages") {
		t.Errorf("must not claim an empty queue outright; got: %s", got)
	}
}

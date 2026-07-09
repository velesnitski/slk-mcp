package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeEmoji(t *testing.T) {
	cases := map[string]string{
		"palm_tree":   ":palm_tree:",
		":palm_tree:": ":palm_tree:",
		"":            "",
		"  coffee  ":  ":coffee:",
	}
	for in, want := range cases {
		if got := normalizeEmoji(in); got != want {
			t.Fatalf("normalizeEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribeStatus(t *testing.T) {
	if got := describeStatus("", ""); got != "cleared custom status" {
		t.Fatalf("empty text+emoji should clear, got %q", got)
	}
	if got := describeStatus("AFK", ":zzz:"); !strings.Contains(got, ":zzz:") || !strings.Contains(got, `"AFK"`) {
		t.Fatalf("should mention emoji and text, got %q", got)
	}
}

func TestStatusExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	if got := statusExpiry(90, now); got != 1000+90*60 {
		t.Fatalf("expiry = %d, want %d", got, 1000+90*60)
	}
	if got := statusExpiry(0, now); got != 0 {
		t.Fatalf("no clear_after must yield 0 (no expiry), got %d", got)
	}
	if got := statusExpiry(-5, now); got != 0 {
		t.Fatalf("negative minutes must yield 0, got %d", got)
	}
}

func TestRunSetStatus_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runSetStatus(context.Background(), statusParams{workspace: "ghost", text: "AFK"}, time.Unix(1000, 0))
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestStatusErrorHint(t *testing.T) {
	if got := statusErrorHint(errors.New("missing_scope")); !strings.Contains(got, "users.profile:write") {
		t.Fatalf("missing_scope should carry the scope fix, got %q", got)
	}
	if got := statusErrorHint(errors.New("ratelimited")); got != "ratelimited" {
		t.Fatalf("other errors pass through, got %q", got)
	}
}

func TestRunSetPresence_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runSetPresence(context.Background(), "ghost", true)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestThreadContextLine_basic(t *testing.T) {
	m := goslack.Message{}
	m.Timestamp = "1747740000.000000" // arbitrary 2025 ts; just for time format
	m.User = "U001"
	m.Text = "the parent message"
	got := ThreadContextLine("↑", m, "Alice")
	if !strings.HasPrefix(got, "\t↑ [") {
		t.Fatalf("expected leading tab + marker, got %q", got)
	}
	if !strings.Contains(got, "Alice] the parent message") {
		t.Fatalf("expected display name + body, got %q", got)
	}
}

func TestThreadContextLine_truncatesLongBody(t *testing.T) {
	m := goslack.Message{}
	m.Text = strings.Repeat("x", 500)
	got := ThreadContextLine("↑", m, "Bob")
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	// Body is the part after "] " — must be at most 203 chars (200 + "...").
	idx := strings.Index(got, "] ")
	if idx < 0 {
		t.Fatalf("malformed line %q", got)
	}
	body := got[idx+2:]
	if len(body) > 203 {
		t.Fatalf("body too long after truncation: %d chars", len(body))
	}
}

func TestThreadContextLine_fallsBackToUserIDWhenNameEmpty(t *testing.T) {
	m := goslack.Message{}
	m.User = "U_FALLBACK"
	m.Text = "hi"
	got := ThreadContextLine("↑", m, "")
	if !strings.Contains(got, "U_FALLBACK] hi") {
		t.Fatalf("expected user-id fallback, got %q", got)
	}
}

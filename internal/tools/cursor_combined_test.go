package tools

import "testing"

func TestParseAfterCursor_PlainAndCombined(t *testing.T) {
	// Plain ts → applies to every workspace.
	perWS, plain := parseAfterCursor("1784012484.471999")
	if perWS != nil || plain != "1784012484.471999" {
		t.Fatalf("plain form: got (%v, %q)", perWS, plain)
	}
	if got := cursorForWorkspace(perWS, plain, "secondary"); got != "1784012484.471999" {
		t.Fatalf("plain cursor must apply to any workspace; got %q", got)
	}

	// Combined form → exact per-workspace cursors, case-insensitive keys.
	perWS, plain = parseAfterCursor("primary=111.1;SECONDARY=222.2")
	if plain != "" || len(perWS) != 2 {
		t.Fatalf("combined form: got (%v, %q)", perWS, plain)
	}
	if got := cursorForWorkspace(perWS, plain, "secondary"); got != "222.2" {
		t.Errorf("secondary cursor: got %q, want 222.2", got)
	}
	if got := cursorForWorkspace(perWS, plain, "Primary"); got != "111.1" {
		t.Errorf("primary cursor (case-insensitive): got %q, want 111.1", got)
	}
	// Unknown workspace in a combined token → no cursor (full sweep there).
	if got := cursorForWorkspace(perWS, plain, "third"); got != "" {
		t.Errorf("unknown workspace should get empty cursor; got %q", got)
	}
}

func TestParseAfterCursor_EdgeCases(t *testing.T) {
	if perWS, plain := parseAfterCursor("  "); perWS != nil || plain != "" {
		t.Errorf("blank: got (%v, %q)", perWS, plain)
	}
	// Comma separator tolerated; malformed pairs skipped, not fatal.
	perWS, _ := parseAfterCursor("primary=1.0,junk,=2.0,secondary=3.0")
	if len(perWS) != 2 || perWS["primary"] != "1.0" || perWS["secondary"] != "3.0" {
		t.Errorf("malformed pairs must be skipped; got %v", perWS)
	}
}

func TestCombinedCursor_RoundTrip(t *testing.T) {
	cursors := map[string]string{"primary": "111.1", "secondary": "222.2", "empty": ""}
	got := combinedCursor([]string{"primary", "secondary", "empty"}, cursors)
	if got != "primary=111.1;secondary=222.2" {
		t.Fatalf("render: got %q", got)
	}
	// The emitted token must parse back to the same cursors.
	perWS, plain := parseAfterCursor(got)
	if plain != "" || perWS["primary"] != "111.1" || perWS["secondary"] != "222.2" {
		t.Fatalf("round-trip: got (%v, %q)", perWS, plain)
	}
	// All-empty → "" so the caller emits no cursor line.
	if got := combinedCursor([]string{"a", "b"}, map[string]string{}); got != "" {
		t.Fatalf("no cursors should render empty; got %q", got)
	}
}

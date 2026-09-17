package slack

import "testing"

// `to:me` is DM-only: Slack scopes `to:` to the message's recipient, so a
// channel message tagging the operator never matches it. A mentions sweep
// built on `to:me` alone therefore reported "no mentions" while a channel
// tag sat unanswered; the handle query is what finds those.
func TestMentionQueries_searchesTheHandleNotJustToMe(t *testing.T) {
	got := MentionQueries("example.person", "2024-04-30")
	want := []string{
		"to:me after:2024-04-30",
		"@example.person after:2024-04-30",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d queries, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("query %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The handle comes back from auth.test without a sigil, but a caller may
// spell it with one. Both must produce the same query: a doubled
// "@@handle" matches nothing and would silently restore the bug.
func TestMentionQueries_handleSigilIsNormalized(t *testing.T) {
	with := MentionQueries("@example.person", "2024-04-30")
	without := MentionQueries("example.person", "2024-04-30")
	if len(with) != 2 || with[1] != without[1] {
		t.Fatalf("a leading @ must not change the query: %v vs %v", with, without)
	}
}

// With no handle (auth.test failed, or returned none) the sweep degrades
// to the old DM-only query rather than searching "@ after:…", which
// matches nothing and would turn a partial answer into an empty one.
func TestMentionQueries_emptyHandleFallsBackToToMe(t *testing.T) {
	for _, handle := range []string{"", "   ", "@"} {
		got := MentionQueries(handle, "2024-04-30")
		if len(got) != 1 || got[0] != "to:me after:2024-04-30" {
			t.Errorf("handle %q: want the to:me query alone, got %v", handle, got)
		}
	}
}

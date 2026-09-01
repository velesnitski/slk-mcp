package slack

import (
	"strings"
	"testing"
)

var workspace = []string{
	"orbit-relay-monitoring",
	"orbit-relay-alerts",
	"platform-main",
	"platform-support",
	"platform-task-alerts",
	"lounge",
	"release-notes",
}

func TestSuggestChannels_ShorthandRanksFirst(t *testing.T) {
	// The miss this exists for: a channel referred to by the prefix
	// everyone says out loud, not by its full name.
	got := suggestChannels("orbit-relay", workspace, 3)
	if len(got) == 0 {
		t.Fatal("a shorthand must produce suggestions")
	}
	if got[0] != "#orbit-relay-alerts" && got[0] != "#orbit-relay-monitoring" {
		t.Fatalf("prefix matches must rank first, got %v", got)
	}
	for _, g := range got {
		if !strings.HasPrefix(g, "#orbit-relay") {
			t.Fatalf("unrelated channel suggested: %v", got)
		}
	}
	// Shortest first within a tier — it is the closest fit.
	if got[0] != "#orbit-relay-alerts" {
		t.Fatalf("shortest prefix match should lead, got %v", got)
	}
}

func TestSuggestChannels_TypoIsCaught(t *testing.T) {
	got := suggestChannels("platform-mian", workspace, 3)
	if len(got) == 0 || got[0] != "#platform-main" {
		t.Fatalf("a one-transposition typo must be caught, got %v", got)
	}
}

func TestSuggestChannels_LongerInputMatchesShorterChannel(t *testing.T) {
	// The caller typed more than the channel is actually called.
	got := suggestChannels("platform-main-alerts", workspace, 3)
	if len(got) == 0 || got[0] != "#platform-main" {
		t.Fatalf("want #platform-main, got %v", got)
	}
}

func TestSuggestChannels_NoWildGuesses(t *testing.T) {
	if got := suggestChannels("completely-unrelated-xyz", workspace, 3); len(got) != 0 {
		t.Fatalf("an unrelated name must not be given suggestions, got %v", got)
	}
	if got := suggestChannels("", workspace, 3); got != nil {
		t.Fatalf("empty input must yield nothing, got %v", got)
	}
	if got := suggestChannels("lounge", workspace, 0); got != nil {
		t.Fatalf("a non-positive limit must yield nothing, got %v", got)
	}
}

func TestSuggestChannels_ExactMatchIsNotSuggested(t *testing.T) {
	// Reaching the miss path with an exact name means something else is
	// wrong; echoing the same name back would be noise.
	for _, g := range suggestChannels("lounge", workspace, 3) {
		if g == "#lounge" {
			t.Fatal("the exact name must not be offered as a suggestion")
		}
	}
}

func TestSuggestChannels_HonoursLimitAndHash(t *testing.T) {
	got := suggestChannels("platform", workspace, 2)
	if len(got) != 2 {
		t.Fatalf("limit must cap the list, got %v", got)
	}
	for _, g := range got {
		if !strings.HasPrefix(g, "#") {
			t.Fatalf("suggestions must be usable verbatim, got %q", g)
		}
	}
}

func TestSuggestChannels_CaseInsensitive(t *testing.T) {
	got := suggestChannels("Orbit-Relay", workspace, 3)
	if len(got) == 0 {
		t.Fatalf("matching must ignore case, got %v", got)
	}
}

func TestEditDistanceWithin(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"main", "mian", 2, true},
		{"main", "man", 2, true},
		{"main", "main", 2, true},
		{"main", "xyzzy", 2, false},
		{"short", "muchlongername", 2, false}, // length gate
		{"привет", "превет", 2, true},         // runes, not bytes
	}
	for _, c := range cases {
		if got := editDistanceWithin(c.a, c.b, c.max); got != c.want {
			t.Errorf("editDistanceWithin(%q,%q,%d) = %v, want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}

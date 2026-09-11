package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestDMCounterparts(t *testing.T) {
	mk := func(id, name string, mpim bool) goslack.SearchMessage {
		var m goslack.SearchMessage
		m.Channel.ID = id
		m.Channel.Name = name
		m.Channel.IsMPIM = mpim
		return m
	}
	got := dmCounterparts([]goslack.SearchMessage{
		mk("C0EXAMPLE01", "example-channel", false),
		mk("D0EXAMPLE01", "U0EXAMPLE99", false),
		mk("D0EXAMPLE01", "U0EXAMPLE99", false), // dedup
		mk("G0EXAMPLE01", "U0EXAMPLE98", true),
		mk("D0EXAMPLE02", "", false), // no id parked in name
	})

	if len(got) != 2 {
		t.Fatalf("want 2 unique counterparts, got %v", got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen["U0EXAMPLE99"] || !seen["U0EXAMPLE98"] {
		t.Fatalf("missing counterpart ids: %v", got)
	}
	if seen["example-channel"] {
		t.Fatal("a channel name must never be resolved as a user id")
	}
}

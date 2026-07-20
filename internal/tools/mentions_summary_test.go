package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func summaryHit(user, channelID, channelName string) goslack.SearchMessage {
	m := goslack.SearchMessage{Username: user, Timestamp: "1.0"}
	m.Channel.ID = channelID
	m.Channel.Name = channelName
	return m
}

func TestSummarizeMentions_CountsAndSplit(t *testing.T) {
	matches := []goslack.SearchMessage{
		summaryHit("jbravo", "D1", "jbravo"),
		summaryHit("jbravo", "D1", "jbravo"),
		summaryHit("jbravo", "C1", "backend"),
		summaryHit("asmith", "D2", "asmith"),
		summaryHit("raven", "C1", "backend"),
	}
	got := summarizeMentions(matches, 120, false)

	for _, want := range []string{
		"5 mentions (last 120h) — summary",
		"split: 3 DM / 2 channel · 3 unique senders",
		"senders: jbravo×3, asmith×1, raven×1", // count desc, tie by name asc
		"channels: #backend×2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got:\n%s", want, got)
		}
	}
	// DM hits must not leak into the channels line.
	if strings.Contains(got, "#jbravo") || strings.Contains(got, "#asmith") {
		t.Errorf("DMs must not appear as channels; got:\n%s", got)
	}
}

func TestSummarizeMentions_PendingHeader(t *testing.T) {
	got := summarizeMentions([]goslack.SearchMessage{summaryHit("a", "D1", "a")}, 24, true)
	if !strings.Contains(got, "pending only") {
		t.Errorf("pending_only must be reflected in the header; got:\n%s", got)
	}
}

func TestTopCounts_CapAndOrder(t *testing.T) {
	counts := map[string]int{"a": 1, "b": 3, "c": 2, "d": 1}
	got := topCounts("senders", counts, 3)
	if got != "senders: b×3, c×2, a×1 (+1 more)" {
		t.Fatalf("got %q", got)
	}
	if topCounts("x", map[string]int{}, 3) != "" {
		t.Fatal("empty map must render empty")
	}
}

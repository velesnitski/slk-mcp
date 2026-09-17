package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

func mentionHit(channelID, ts, user string) goslack.SearchMessage {
	var m goslack.SearchMessage
	m.Channel.ID = channelID
	m.Timestamp = ts
	m.User = user
	return m
}

// The handle query matches every message carrying the operator's handle,
// including the ones they wrote. Rendering those as mentions tells the
// operator someone pinged them when the only person who did was them.
func TestFilterOwnMessages_dropsTheOperatorsOwnLines(t *testing.T) {
	matches := []goslack.SearchMessage{
		mentionHit("C0EXAMPLE01", "1714492800.000100", "U0EXAMPLE01"),
		mentionHit("C0EXAMPLE01", "1714492800.000200", "U0EXAMPLE02"),
	}
	got := filterOwnMessages(matches, "U0EXAMPLE01")
	if len(got) != 1 {
		t.Fatalf("want 1 surviving mention, got %d", len(got))
	}
	if got[0].User != "U0EXAMPLE02" {
		t.Errorf("kept the wrong message: %+v", got[0])
	}
}

// An unknown self id means we cannot tell whose message is whose.
// Dropping everything would empty the sweep, so the safe direction is to
// drop nothing.
func TestFilterOwnMessages_emptySelfIDKeepsEverything(t *testing.T) {
	matches := []goslack.SearchMessage{
		mentionHit("C0EXAMPLE01", "1714492800.000100", "U0EXAMPLE01"),
	}
	if got := filterOwnMessages(matches, ""); len(got) != 1 {
		t.Fatalf("want the set untouched, got %d", len(got))
	}
}

// The two mention queries overlap: a DM that also spells the handle comes
// back from both. Merging must list it once, newest-first.
func TestMergeSearchHits_dedupesAcrossTheTwoMentionQueries(t *testing.T) {
	toMe := []goslack.SearchMessage{
		mentionHit("D0EXAMPLE01", "1714492800.000100", "U0EXAMPLE02"),
	}
	byHandle := []goslack.SearchMessage{
		mentionHit("D0EXAMPLE01", "1714492800.000100", "U0EXAMPLE02"),
		mentionHit("C0EXAMPLE01", "1714492800.000300", "U0EXAMPLE02"),
	}
	got := mergeSearchHits(toMe, byHandle)
	if len(got) != 2 {
		t.Fatalf("want 2 unique hits, got %d", len(got))
	}
	if got[0].Timestamp != "1714492800.000300" {
		t.Errorf("want newest-first ordering, got %q first", got[0].Timestamp)
	}
}

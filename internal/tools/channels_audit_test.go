package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func mkChan(name string, members int, isMember, isPrivate bool, topic, purpose string) goslack.Channel {
	ch := goslack.Channel{}
	ch.Name = name
	ch.NumMembers = members
	ch.IsMember = isMember
	ch.IsPrivate = isPrivate
	ch.Topic.Value = topic
	ch.Purpose.Value = purpose
	return ch
}

func TestFilterUnjoined_offReturnsInputUnchanged(t *testing.T) {
	in := []goslack.Channel{
		mkChan("a", 10, true, false, "", ""),
		mkChan("b", 5, false, false, "", ""),
	}
	out := filterUnjoined(in, false)
	if len(out) != 2 {
		t.Fatalf("filter off must return everything; got %d", len(out))
	}
}

func TestFilterUnjoined_keepsOnlyNonMembers(t *testing.T) {
	in := []goslack.Channel{
		mkChan("a", 10, true, false, "", ""),
		mkChan("b", 5, false, false, "", ""),
		mkChan("c", 3, true, false, "", ""),
		mkChan("d", 1, false, true, "", ""),
	}
	out := filterUnjoined(in, true)
	if len(out) != 2 {
		t.Fatalf("expected 2 unjoined entries; got %d", len(out))
	}
	for _, ch := range out {
		if ch.IsMember {
			t.Fatalf("unjoined filter let a member through: %s", ch.Name)
		}
	}
}

func TestRenderChannelLine_silentForJoinedPublic(t *testing.T) {
	// The common case — joined public channel with a topic. No
	// marker noise; just the core line.
	ch := mkChan("general", 85, true, false, "Workspace announcements", "")
	got := renderChannelLine(ch)
	want := "- #general (85) Workspace announcements"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderChannelLine_markersForUnjoinedPrivate(t *testing.T) {
	ch := mkChan("secret-team", 4, false, true, "Plans", "")
	got := renderChannelLine(ch)
	// Must contain both 🔒 and [NOT JOINED] in the right order.
	if !strings.Contains(got, "#secret-team 🔒 (4) [NOT JOINED]") {
		t.Fatalf("markers missing or wrong order; got %q", got)
	}
}

func TestRenderChannelLine_fallsBackFromTopicToPurpose(t *testing.T) {
	// Topic empty, purpose set — purpose should surface so an
	// audit-time reader knows what the channel is for.
	ch := mkChan("billing-alerts", 3, false, false, "", "Automated alerts from billing pipeline")
	got := renderChannelLine(ch)
	if !strings.Contains(got, "Automated alerts from billing pipeline") {
		t.Fatalf("purpose fallback missing; got %q", got)
	}
}

func TestRenderChannelLine_truncatesLongContext(t *testing.T) {
	long := strings.Repeat("x", 200)
	ch := mkChan("c1", 1, true, false, long, "")
	got := renderChannelLine(ch)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncation marker; got %q", got)
	}
	if len(got) > 100 {
		t.Fatalf("line shouldn't run unbounded; got %d chars", len(got))
	}
}

func TestRenderChannelLine_emptyTopicAndPurposeNoTrailingSpace(t *testing.T) {
	// A channel with neither topic nor purpose should not produce
	// trailing whitespace — easy to grep / pipe.
	ch := mkChan("c1", 1, true, false, "", "")
	got := renderChannelLine(ch)
	if got != "- #c1 (1)" {
		t.Fatalf("expected bare line, got %q", got)
	}
}

func TestRenderChannelLine_publicJoinedWithEmptyContext(t *testing.T) {
	ch := mkChan("random", 50, true, false, "", "")
	got := renderChannelLine(ch)
	if got != "- #random (50)" {
		t.Fatalf("got %q", got)
	}
}

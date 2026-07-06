package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func msgAt(ts, text string) goslack.Message {
	return goslack.Message{Msg: goslack.Msg{Timestamp: ts, Text: text}}
}

func mkChannelWithMsgs(id string, msgs ...goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{}
	cu.Channel.ID = id
	cu.Messages = msgs
	return cu
}

func TestTsAfter(t *testing.T) {
	if !tsAfter("100.5", "100.0") {
		t.Fatal("newer ts must be after")
	}
	if tsAfter("100.0", "100.5") {
		t.Fatal("older ts must not be after")
	}
	if tsAfter("100.0", "100.0") {
		t.Fatal("equal ts must not be after (strict)")
	}
	if !tsAfter("garbage", "100.0") {
		t.Fatal("unplaceable message ts fails open (kept)")
	}
	if !tsAfter("100.0", "garbage") {
		t.Fatal("unusable cursor keeps everything")
	}
}

func TestFilterAfter_emptyCursorIsNoop(t *testing.T) {
	in := []*slack.ChannelUnread{mkChannelWithMsgs("C1", msgAt("1.0", "a"))}
	got := filterAfter(in, "")
	if len(got) != 1 || len(got[0].Messages) != 1 {
		t.Fatalf("empty cursor must pass through unchanged; got %+v", got)
	}
}

func TestFilterAfter_dropsOldMessagesAndEmptyChannels(t *testing.T) {
	results := []*slack.ChannelUnread{
		mkChannelWithMsgs("C_OLD", msgAt("100.0", "old"), msgAt("101.0", "old2")),
		mkChannelWithMsgs("C_MIX", msgAt("100.0", "old"), msgAt("200.0", "new")),
	}
	got := filterAfter(results, "150.0")
	if len(got) != 1 {
		t.Fatalf("channel with only old messages must be dropped; got %d channels", len(got))
	}
	if got[0].Channel.ID != "C_MIX" || len(got[0].Messages) != 1 || got[0].Messages[0].Text != "new" {
		t.Fatalf("only the newer message should survive; got %+v", got[0])
	}
}

func TestFilterAfter_keepsChannelOnFreshReplyToOldParent(t *testing.T) {
	// A reply to an already-read parent is still fresh signal — the
	// channel must survive even though its top-level messages are old.
	cu := mkChannelWithMsgs("C1", msgAt("100.0", "old parent"))
	cu.Replies = map[string][]goslack.Message{
		"100.0": {msgAt("140.0", "stale reply"), msgAt("200.0", "fresh reply")},
	}
	got := filterAfter([]*slack.ChannelUnread{cu}, "150.0")
	if len(got) != 1 {
		t.Fatalf("channel with a fresh reply must survive; got %d", len(got))
	}
	if len(got[0].Messages) != 0 {
		t.Fatal("old top-level message should have been dropped")
	}
	kept := got[0].Replies["100.0"]
	if len(kept) != 1 || kept[0].Text != "fresh reply" {
		t.Fatalf("only the fresh reply should remain; got %+v", kept)
	}
}

func TestNewestTS(t *testing.T) {
	cu := mkChannelWithMsgs("C1", msgAt("100.0", "a"), msgAt("300.5", "c"))
	cu.Replies = map[string][]goslack.Message{"100.0": {msgAt("250.0", "r")}}
	other := mkChannelWithMsgs("C2", msgAt("200.0", "b"))

	if got := newestTS([]*slack.ChannelUnread{cu, other}); got != "300.5" {
		t.Fatalf("newestTS = %q, want 300.5", got)
	}
	if got := newestTS(nil); got != "" {
		t.Fatalf("newestTS(nil) = %q, want empty", got)
	}
}

func TestNewestTS_roundTripsThroughFilterAfter(t *testing.T) {
	// The contract that makes the delta loop work: feeding newestTS
	// back as the cursor drops everything (nothing is strictly newer
	// than the newest).
	results := []*slack.ChannelUnread{
		mkChannelWithMsgs("C1", msgAt("100.0", "a"), msgAt("200.0", "b")),
		mkChannelWithMsgs("C2", msgAt("300.0", "c")),
	}
	cursor := newestTS(results)
	if got := filterAfter(results, cursor); len(got) != 0 {
		t.Fatalf("re-pull with the emitted cursor should yield nothing new; got %d channels", len(got))
	}
}

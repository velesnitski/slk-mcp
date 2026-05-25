package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func mkChannelWithReplies(id string, replies map[string][]goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{}
	cu.Channel.ID = id
	cu.Replies = replies
	return cu
}

func TestMergeThreadMentions_emptyMentions(t *testing.T) {
	base := []*slack.ChannelUnread{mkChannel("C1", false, false)}
	got := mergeThreadMentions(base, nil)
	if len(got) != 1 || got[0].Channel.ID != "C1" {
		t.Fatalf("empty mentions must pass base through; got %v", got)
	}
}

func TestMergeThreadMentions_appendsNewChannel(t *testing.T) {
	// The whole point: a channel the unread sweep didn't surface
	// (because the thread parent was already read) but the search
	// backstop found a reply mentioning the operator.
	base := []*slack.ChannelUnread{mkChannel("C_OTHER", false, false)}
	mention := mkChannelWithReplies("C_TARGET", map[string][]goslack.Message{
		"100.0": {{Msg: goslack.Msg{Text: "ping <@U_SELF>", Timestamp: "100.5"}}},
	})
	got := mergeThreadMentions(base, []*slack.ChannelUnread{mention})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (base + appended); got %d", len(got))
	}
	if got[1].Channel.ID != "C_TARGET" {
		t.Fatalf("expected appended C_TARGET; got %s", got[1].Channel.ID)
	}
}

func TestMergeThreadMentions_mergesIntoExistingChannel(t *testing.T) {
	// The channel WAS in base (other unread activity), so the mention
	// reply must merge into the existing entry — not create a duplicate.
	base := []*slack.ChannelUnread{mkChannelWithReplies("C1", map[string][]goslack.Message{
		"100.0": {{Msg: goslack.Msg{Text: "earlier reply", Timestamp: "100.1"}}},
	})}
	mention := mkChannelWithReplies("C1", map[string][]goslack.Message{
		"100.0": {{Msg: goslack.Msg{Text: "ping <@U_SELF>", Timestamp: "100.2"}}},
	})
	got := mergeThreadMentions(base, []*slack.ChannelUnread{mention})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after merge; got %d", len(got))
	}
	bucket := got[0].Replies["100.0"]
	if len(bucket) != 2 {
		t.Fatalf("expected 2 replies in bucket; got %d", len(bucket))
	}
}

func TestMergeThreadMentions_dedupesByTimestamp(t *testing.T) {
	// Slack search occasionally returns the same hit twice across
	// sweeps; the merge must dedupe by timestamp so we never double-
	// count a single mention.
	base := []*slack.ChannelUnread{mkChannelWithReplies("C1", map[string][]goslack.Message{
		"100.0": {{Msg: goslack.Msg{Text: "ping <@U_SELF>", Timestamp: "100.5"}}},
	})}
	mention := mkChannelWithReplies("C1", map[string][]goslack.Message{
		"100.0": {{Msg: goslack.Msg{Text: "ping <@U_SELF>", Timestamp: "100.5"}}},
	})
	got := mergeThreadMentions(base, []*slack.ChannelUnread{mention})
	if got[0].Replies["100.0"] == nil || len(got[0].Replies["100.0"]) != 1 {
		t.Fatalf("expected dedupe to 1 reply; got %v", got[0].Replies["100.0"])
	}
}

func TestMergeThreadMentions_skipsNilAndEmptyID(t *testing.T) {
	base := []*slack.ChannelUnread{mkChannel("C1", false, false)}
	mentions := []*slack.ChannelUnread{nil, mkChannel("", false, false)}
	got := mergeThreadMentions(base, mentions)
	if len(got) != 1 {
		t.Fatalf("nil + empty-ID mentions must be skipped; got %d entries", len(got))
	}
}

func TestMergeThreadMentions_topLevelMessagesMerge(t *testing.T) {
	// If the mention came as a top-level @-mention (not a reply), it
	// lands in Messages rather than Replies. Same dedup rules apply.
	baseCh := mkChannel("C1", false, false)
	baseCh.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "old", Timestamp: "1.0"}},
	}
	mention := mkChannel("C1", false, false)
	mention.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "old", Timestamp: "1.0"}},                  // dup — must skip
		{Msg: goslack.Msg{Text: "fresh ping <@U_SELF>", Timestamp: "2.0"}}, // new
	}
	got := mergeThreadMentions([]*slack.ChannelUnread{baseCh}, []*slack.ChannelUnread{mention})
	if len(got[0].Messages) != 2 {
		t.Fatalf("expected 2 messages after dedup; got %d", len(got[0].Messages))
	}
}

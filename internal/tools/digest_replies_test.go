package tools

import (
	"context"
	"errors"
	"testing"

	goslack "github.com/slack-go/slack"
)

func parentMsg(ts string, replyCount int) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.ThreadTimestamp = ts
	m.ReplyCount = replyCount
	return m
}

func plainMsg(ts string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	return m
}

func TestCollectThreadReplies_FetchesParentsStripsParent(t *testing.T) {
	reply := plainMsg("100.5")
	reply.Text = "the actual discussion"
	fake := &fakeMsgClient{
		WantChannelID: "C1",
		Replies: map[string][]goslack.Message{
			// conversations.replies returns the parent first — it must be
			// stripped from the collected replies.
			"100.0": {parentMsg("100.0", 1), reply},
		},
	}
	window := []goslack.Message{
		parentMsg("100.0", 1), // thread parent → fetch
		plainMsg("200.0"),     // no thread → skip
	}
	got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", window)
	if len(got) != 1 || len(got["100.0"]) != 1 || got["100.0"][0].Text != "the actual discussion" {
		t.Fatalf("want one stripped reply under 100.0; got %+v", got)
	}
	if fake.RepliesCalls != 1 {
		t.Fatalf("only the thread parent should trigger a replies call; got %d", fake.RepliesCalls)
	}
}

func TestCollectThreadReplies_BestEffortOnError(t *testing.T) {
	fake := &fakeMsgClient{ErrReplies: errors.New("boom")}
	window := []goslack.Message{parentMsg("100.0", 2)}
	if got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", window); got != nil {
		t.Fatalf("unreadable thread must be skipped, not fatal; got %+v", got)
	}
}

func TestCollectThreadReplies_NoParentsNoCalls(t *testing.T) {
	fake := &fakeMsgClient{}
	window := []goslack.Message{plainMsg("1.0"), plainMsg("2.0")}
	if got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", window); got != nil {
		t.Fatalf("no thread parents → nil; got %+v", got)
	}
	if fake.RepliesCalls != 0 {
		t.Fatalf("no replies calls expected; got %d", fake.RepliesCalls)
	}
}

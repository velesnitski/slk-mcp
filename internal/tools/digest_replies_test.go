package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

// The synthetic epoch used across this repo's fixtures.
var testWindowStart = time.Unix(1714492800, 0)

const (
	tsBeforeWindow = "1714406400.000000" // 24h before the window
	tsStaleReply   = "1714410000.000000" // still before the window
	tsInWindow     = "1714496400.000000" // 1h after the window opens
)

func parentMsg(ts string, replyCount int) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.ThreadTimestamp = ts
	m.ReplyCount = replyCount
	return m
}

func parentWithLatest(ts string, replyCount int, latestReply string) goslack.Message {
	m := parentMsg(ts, replyCount)
	m.LatestReply = latestReply
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
	page := []goslack.Message{
		parentMsg("100.0", 1), // thread parent → fetch
		plainMsg("200.0"),     // no thread → skip
	}
	got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", page, time.Time{}, time.Time{})
	if len(got) != 1 || len(got["100.0"]) != 1 || got["100.0"][0].Text != "the actual discussion" {
		t.Fatalf("want one stripped reply under 100.0; got %+v", got)
	}
	if fake.RepliesCalls != 1 {
		t.Fatalf("only the thread parent should trigger a replies call; got %d", fake.RepliesCalls)
	}
}

// The defect this closes. A reply posted inside the window belongs to a
// thread started before it, so the parent is off the history page for
// any window that does not reach back to it — and conversations.replies
// cannot be called without a root timestamp. The channel then renders as
// nothing while it is plainly active.
func TestCollectThreadReplies_FindsRepliesUnderAnOlderParent(t *testing.T) {
	reply := plainMsg(tsInWindow)
	reply.Text = "the reply that mattered"
	older := parentWithLatest(tsBeforeWindow, 1, tsInWindow)

	fake := &fakeMsgClient{
		WantChannelID: "C1",
		Replies: map[string][]goslack.Message{
			tsBeforeWindow: {older, reply},
		},
	}
	got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1",
		[]goslack.Message{older}, testWindowStart, time.Time{})

	if len(got[tsBeforeWindow]) != 1 {
		t.Fatalf("a reply under an older parent must still be collected; got %+v", got)
	}
	if got[tsBeforeWindow][0].Text != "the reply that mattered" {
		t.Errorf("wrong reply collected: %+v", got[tsBeforeWindow])
	}
}

// Reaching back for parents must not cost one API call per stale thread:
// latest_reply already says whether a thread could have moved.
func TestCollectThreadReplies_SkipsThreadsThatDidNotMove(t *testing.T) {
	stale := parentWithLatest(tsBeforeWindow, 3, tsStaleReply)
	fake := &fakeMsgClient{}
	got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1",
		[]goslack.Message{stale}, testWindowStart, time.Time{})
	if got != nil {
		t.Fatalf("a thread whose newest reply predates the window must be skipped; got %+v", got)
	}
	if fake.RepliesCalls != 0 {
		t.Fatalf("latest_reply must spare the replies call; got %d", fake.RepliesCalls)
	}
}

// A moved thread still carries its whole history; only the part inside
// the window belongs in a windowed digest.
func TestCollectThreadReplies_DropsRepliesOutsideTheWindow(t *testing.T) {
	old := plainMsg(tsStaleReply)
	old.Text = "old"
	fresh := plainMsg(tsInWindow)
	fresh.Text = "new"
	parent := parentWithLatest(tsBeforeWindow, 2, tsInWindow)

	fake := &fakeMsgClient{Replies: map[string][]goslack.Message{
		tsBeforeWindow: {parent, old, fresh},
	}}
	got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1",
		[]goslack.Message{parent}, testWindowStart, time.Time{})

	reps := got[tsBeforeWindow]
	if len(reps) != 1 || reps[0].Text != "new" {
		t.Fatalf("only in-window replies belong in a windowed digest; got %+v", reps)
	}
}

func TestCollectThreadReplies_BestEffortOnError(t *testing.T) {
	fake := &fakeMsgClient{ErrReplies: errors.New("boom")}
	page := []goslack.Message{parentMsg("100.0", 2)}
	if got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", page, time.Time{}, time.Time{}); got != nil {
		t.Fatalf("unreadable thread must be skipped, not fatal; got %+v", got)
	}
}

func TestCollectThreadReplies_NoParentsNoCalls(t *testing.T) {
	fake := &fakeMsgClient{}
	page := []goslack.Message{plainMsg("1.0"), plainMsg("2.0")}
	if got := collectThreadReplies(context.Background(), &fakeMessageClient{fake}, "C1", page, time.Time{}, time.Time{}); got != nil {
		t.Fatalf("no thread parents → nil; got %+v", got)
	}
	if fake.RepliesCalls != 0 {
		t.Fatalf("no replies calls expected; got %d", fake.RepliesCalls)
	}
}

// An absent latest_reply must fail open: dropping a live thread because
// a field was missing is the failure mode this whole change exists to
// remove.
func TestThreadMovedInWindow_missingLatestReplyFailsOpen(t *testing.T) {
	if !threadMovedInWindow(parentMsg(tsBeforeWindow, 2), testWindowStart) {
		t.Fatal("an absent latest_reply must not silently drop a thread")
	}
	unreadable := parentWithLatest(tsBeforeWindow, 2, "not-a-timestamp")
	if !threadMovedInWindow(unreadable, testWindowStart) {
		t.Fatal("an unparseable latest_reply must not silently drop a thread")
	}
}

func TestTsWithin_bounds(t *testing.T) {
	start := time.Unix(1714492800, 0)
	end := time.Unix(1714579200, 0)
	cases := []struct {
		ts   string
		want bool
	}{
		{"1714492800.000000", true},  // exactly at the lower bound
		{"1714492799.000000", false}, // one second before it
		{"1714579200.000000", true},  // exactly at the upper bound
		{"1714579201.000000", false}, // one second past it
		{"not-a-timestamp", true},    // unreadable: keep rather than drop
	}
	for _, c := range cases {
		if got := tsWithin(c.ts, start, end); got != c.want {
			t.Errorf("tsWithin(%q) = %v, want %v", c.ts, got, c.want)
		}
	}
	if !tsWithin("1714579201.000000", start, time.Time{}) {
		t.Error("a zero upper bound means no upper bound")
	}
}

func TestCountThreadsMovedInWindow(t *testing.T) {
	page := []goslack.Message{
		parentWithLatest(tsBeforeWindow, 1, tsInWindow),        // moved into the window
		parentWithLatest("1714406401.000000", 1, tsStaleReply), // stale
		plainMsg("1714406402.000000"),                          // not a thread at all
	}
	if n := countThreadsMovedInWindow(page, testWindowStart); n != 1 {
		t.Fatalf("want exactly 1 moved thread, got %d", n)
	}
}

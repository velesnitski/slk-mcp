package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	goslack "github.com/slack-go/slack"
)

// newTestUnreadServiceWithSearch wires both the unread service AND the
// search service to the same fake server, so tests for the search-
// based thread-mention backstop can register search.messages handlers.
func newTestUnreadServiceWithSearch(t *testing.T, f *fakeSlack) *UnreadService {
	t.Helper()
	api := goslack.New("xoxp-test", goslack.OptionAPIURL(f.apiURL()))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := newUserService(api, log)
	channels := newChannelService(api, users, log)
	search := newSearchService(api, log)
	return newUnreadService(api, channels, users, search, log)
}

func TestUnreadThreadMentions_returnsNilOnZeroHours(t *testing.T) {
	f := newFakeSlack(t)
	s := newTestUnreadServiceWithSearch(t, f)
	out, err := s.UnreadThreadMentions(context.Background(), 0)
	if err != nil {
		t.Fatalf("hours=0 must not error; got %v", err)
	}
	if out != nil {
		t.Fatalf("hours=0 must return nil; got %v", out)
	}
}

func TestUnreadThreadMentions_returnsNilWhenSearchAbsent(t *testing.T) {
	// A misconfigured service (no search wired) must not panic — it
	// should return (nil, nil) so the caller's fallback is the
	// pre-existing unread-only contract.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := goslack.New("xoxp-test")
	s := newUnreadService(api, nil, nil, nil, log)
	out, err := s.UnreadThreadMentions(context.Background(), 24)
	if err != nil {
		t.Fatalf("missing search service must not error; got %v", err)
	}
	if out != nil {
		t.Fatalf("missing search service must return nil; got %v", out)
	}
}

func TestUnreadThreadMentions_groupsByChannel(t *testing.T) {
	f := newFakeSlack(t)
	prev := nowUnixFn
	nowUnixFn = func() int64 { return 1700100000 }
	t.Cleanup(func() { nowUnixFn = prev })

	// Two mentions in the same channel + one in a different channel.
	// Result must collapse to 2 entries, not 3.
	f.on("search.messages", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"messages": map[string]any{
				"total": 3,
				"matches": []map[string]any{
					{
						"type":      "message",
						"channel":   map[string]any{"id": "C_TARGET", "name": "qa"},
						"user":      "U2",
						"text":      "ping <@U_SELF>",
						"ts":        "1700099000.000000",
						"permalink": "https://x.slack.com/archives/C_TARGET/p1700099000?thread_ts=1700050000.000000",
					},
					{
						"type":      "message",
						"channel":   map[string]any{"id": "C_TARGET", "name": "qa"},
						"user":      "U3",
						"text":      "follow-up <@U_SELF>",
						"ts":        "1700099500.000000",
						"permalink": "https://x.slack.com/archives/C_TARGET/p1700099500?thread_ts=1700050000.000000",
					},
					{
						"type":      "message",
						"channel":   map[string]any{"id": "C_OTHER", "name": "other"},
						"user":      "U4",
						"text":      "topic mention <@U_SELF>",
						"ts":        "1700099800.000000",
						"permalink": "https://x.slack.com/archives/C_OTHER/p1700099800",
					},
				},
			},
		}
	})

	s := newTestUnreadServiceWithSearch(t, f)
	out, err := s.UnreadThreadMentions(context.Background(), 1)
	if err != nil {
		t.Fatalf("UnreadThreadMentions err: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 channels after grouping; got %d", len(out))
	}
	gotByID := map[string]*ChannelUnread{}
	for _, cu := range out {
		gotByID[cu.Channel.ID] = cu
	}
	target := gotByID["C_TARGET"]
	if target == nil {
		t.Fatal("C_TARGET missing from result")
	}
	// Both C_TARGET hits had thread_ts in their permalink → they
	// went into Replies, not Messages.
	if len(target.Replies["1700050000.000000"]) != 2 {
		t.Fatalf("expected 2 replies under thread; got %d", len(target.Replies["1700050000.000000"]))
	}
	other := gotByID["C_OTHER"]
	if other == nil {
		t.Fatal("C_OTHER missing from result")
	}
	// C_OTHER hit had no thread_ts → top-level Messages.
	if len(other.Messages) != 1 {
		t.Fatalf("expected 1 top-level message; got %d", len(other.Messages))
	}
}

func TestUnreadThreadMentions_filtersHourGranularLeakage(t *testing.T) {
	// Slack `after:` is date-granular, so messages from earlier the
	// same day (e.g. 02:00) leak in when the cutoff is 18:00. The
	// post-fetch filter must drop those.
	f := newFakeSlack(t)
	prev := nowUnixFn
	// Pin "now" so the cutoff math is deterministic.
	nowUnixFn = func() int64 { return 1700100000 } // 1h cutoff = 1700096400
	t.Cleanup(func() { nowUnixFn = prev })

	f.on("search.messages", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"messages": map[string]any{
				"total": 2,
				"matches": []map[string]any{
					{
						"type":      "message",
						"channel":   map[string]any{"id": "C1"},
						"user":      "U2",
						"text":      "early in day",
						"ts":        "1700050000.000000", // before cutoff
						"permalink": "https://x.slack.com/archives/C1/p1700050000",
					},
					{
						"type":      "message",
						"channel":   map[string]any{"id": "C1"},
						"user":      "U2",
						"text":      "in window",
						"ts":        "1700099500.000000", // after cutoff
						"permalink": "https://x.slack.com/archives/C1/p1700099500",
					},
				},
			},
		}
	})

	s := newTestUnreadServiceWithSearch(t, f)
	out, err := s.UnreadThreadMentions(context.Background(), 1)
	if err != nil {
		t.Fatalf("UnreadThreadMentions err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 channel; got %d", len(out))
	}
	if len(out[0].Messages) != 1 {
		t.Fatalf("expected only the in-window hit; got %d", len(out[0].Messages))
	}
	if out[0].Messages[0].Text != "in window" {
		t.Fatalf("wrong hit survived filter; got %q", out[0].Messages[0].Text)
	}
}

func TestSearchHitToMessage_parsesThreadTS(t *testing.T) {
	// Conversion helper: a permalink with ?thread_ts=... must produce
	// a Message whose ThreadTimestamp is set, so downstream code can
	// distinguish a reply from a top-level message.
	h := goslack.SearchMessage{}
	h.User = "U1"
	h.Text = "hi"
	h.Timestamp = "100.5"
	h.Permalink = "https://x.slack.com/archives/C1/p100500?thread_ts=100.000000&cid=C1"
	m := searchHitToMessage(h)
	if m.ThreadTimestamp != "100.000000" {
		t.Fatalf("expected ThreadTimestamp=100.000000; got %q", m.ThreadTimestamp)
	}
}

func TestSearchHitToMessage_noThreadTSForTopLevel(t *testing.T) {
	h := goslack.SearchMessage{}
	h.Timestamp = "200.0"
	h.Permalink = "https://x.slack.com/archives/C1/p200000"
	m := searchHitToMessage(h)
	if m.ThreadTimestamp != "" {
		t.Fatalf("top-level hit must not set ThreadTimestamp; got %q", m.ThreadTimestamp)
	}
}

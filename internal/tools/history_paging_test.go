package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 111. Every earlier history fake answered conversations.history with
// a fixed body and ignored `oldest`, `latest` and `limit` — so no test
// could observe WHICH slice of a conversation a request returns, and code
// built on a wrong model of Slack's paging passed all of them. This fake
// pages the way the live API was measured to:
//
//   - `latest` set (or neither bound)  → the `limit` messages adjacent to
//     `latest` (the newest), inclusive of `latest`;
//   - `oldest` set without `latest`    → the `limit` messages adjacent to
//     `oldest` (the OLDEST in range).
//
// Pages are returned newest-first in both cases, as Slack does.

type pgMsg struct {
	TS          string `json:"ts"`
	User        string `json:"user"`
	Text        string `json:"text"`
	Type        string `json:"type"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	ReplyCount  int    `json:"reply_count,omitempty"`
	LatestReply string `json:"latest_reply,omitempty"`
}

// pgAgo is a Slack ts `d` before now, with a sequence number in the
// microseconds so messages in the same second stay distinct and ordered.
func pgAgo(d time.Duration, seq int) string {
	return fmt.Sprintf("%d.%06d", time.Now().Add(-d).Unix(), seq)
}

func pgF(ts string) float64 { f, _ := strconv.ParseFloat(ts, 64); return f }

// pgHistory serves msgs with Slack's measured paging and records every
// request's bounds so a test can also assert on the call pattern.
func pgHistory(t *testing.T, f *fakeSlack, msgs []pgMsg) *[]map[string]string {
	t.Helper()
	sorted := append([]pgMsg(nil), msgs...)
	sort.Slice(sorted, func(i, j int) bool { return pgF(sorted[i].TS) < pgF(sorted[j].TS) })
	var calls []map[string]string
	f.OnFunc("conversations.history", func(r *http.Request) string {
		oldest, latest := r.Form.Get("oldest"), r.Form.Get("latest")
		calls = append(calls, map[string]string{"oldest": oldest, "latest": latest, "limit": r.Form.Get("limit")})
		limit, _ := strconv.Atoi(r.Form.Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		var in []pgMsg
		for _, m := range sorted {
			ts := pgF(m.TS)
			if oldest != "" && ts <= pgF(oldest) {
				continue
			}
			if latest != "" && ts > pgF(latest) {
				continue
			}
			in = append(in, m)
		}
		more := len(in) > limit
		if more {
			if oldest != "" && latest == "" {
				in = in[:limit] // adjacent to oldest: the stale end
			} else {
				in = in[len(in)-limit:] // adjacent to latest: the fresh end
			}
		}
		for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
			in[i], in[j] = in[j], in[i] // newest-first, as Slack returns
		}
		body, _ := json.Marshal(map[string]any{"ok": true, "has_more": more, "messages": in})
		return string(body)
	})
	return &calls
}

// pgBusyWeek is a conversation with `n` messages spread evenly over the
// last week — far more than one page — whose newest message is `last`.
func pgBusyWeek(n int, last string) []pgMsg {
	var out []pgMsg
	step := 7 * 24 * time.Hour / time.Duration(n)
	for i := n; i >= 1; i-- {
		out = append(out, pgMsg{Type: "message", User: "U2", TS: pgAgo(time.Duration(i)*step, i), Text: fmt.Sprintf("old chatter %d", i)})
	}
	out = append(out, pgMsg{Type: "message", User: "U2", TS: pgAgo(5*time.Minute, 0), Text: last})
	return out
}

func pgHub(t *testing.T, f *fakeSlack, limit int) *Hub {
	t.Helper()
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	tmAuthTest(f, "U1", tmHost+"/")
	return newFakeHub(t, f, func(c *config.Config) {
		c.MaxMessagesPerChannel = limit
		c.DecisionKeywords = []string{"decided"}
	})
}

// ------------------------------------------------------------------ digest

func TestDigest_BusyConversationShowsTodayNotLastWeek(t *testing.T) {
	// The reported defect: a DM with hundreds of messages a week rendered
	// "(no activity)" for the last 6 hours, because the page anchored at
	// `oldest - 7 days` held only last week.
	f := newFakeSlack(t)
	pgHistory(t, f, pgBusyWeek(400, "the message from five minutes ago"))
	h := pgHub(t, f, 50)

	out, err := h.channelDigestRange(context.Background(), "alpha",
		time.Now().Add(-6*time.Hour), time.Time{}, 50, true, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the message from five minutes ago") {
		t.Fatalf("today's message must be in a 6h digest of a busy conversation:\n%s", out)
	}
	if strings.Contains(out, "(no activity)") {
		t.Fatalf("a busy conversation must not read as quiet:\n%s", out)
	}
}

func TestDigest_WindowFetchIsAnchoredAtItsUpperEdge(t *testing.T) {
	f := newFakeSlack(t)
	calls := pgHistory(t, f, pgBusyWeek(10, "recent"))
	h := pgHub(t, f, 50)

	_, _ = h.channelDigestRange(context.Background(), "alpha",
		time.Now().Add(-6*time.Hour), time.Time{}, 50, false, false, 0, false)

	if len(*calls) == 0 || (*calls)[0]["oldest"] != "" {
		t.Fatalf("the window page must not be anchored at a lower bound; calls=%v", *calls)
	}
}

func TestDigest_ThreadParentBeforeTheWindowIsStillFoundInABusyConversation(t *testing.T) {
	// ADR 106 must survive the fix: a reply inside the window to a thread
	// started before it is reachable — now through a second page fetched
	// downward from the window's edge, not by moving `oldest` back.
	f := newFakeSlack(t)
	msgs := pgBusyWeek(400, "unrelated top-level")[:400] // drop the in-window top-level message
	parentTS := pgAgo(8*time.Hour, 7)
	replyTS := pgAgo(10*time.Minute, 8)
	msgs = append(msgs, pgMsg{Type: "message", User: "U2", TS: parentTS, ThreadTS: parentTS,
		ReplyCount: 1, LatestReply: replyTS, Text: "the old question"})
	pgHistory(t, f, msgs)
	f.On("conversations.replies", fmt.Sprintf(`{"ok":true,"messages":[
		{"type":"message","user":"U2","ts":%q,"thread_ts":%q,"text":"the old question"},
		{"type":"message","user":"U1","ts":%q,"thread_ts":%q,"text":"the fresh answer"}]}`,
		parentTS, parentTS, replyTS, parentTS))
	h := pgHub(t, f, 50)

	out, err := h.channelDigestRange(context.Background(), "alpha",
		time.Now().Add(-6*time.Hour), time.Time{}, 50, true, true, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the fresh answer") {
		t.Fatalf("a reply in the window to an older thread must be shown:\n%s", out)
	}
}

// ------------------------------------------------------- duplicate guard

func TestRecentSelfDuplicate_SeesTheOwnPostInABusyChannel(t *testing.T) {
	// Before the fix the guard fetched the page anchored at `now - 30m`:
	// in a channel with more than 100 messages in half an hour it held the
	// oldest 100 and missed the operator's post from a minute ago — the
	// very duplicate it exists to catch.
	f := newFakeSlack(t)
	var msgs []pgMsg
	for i := 0; i < 250; i++ {
		msgs = append(msgs, pgMsg{Type: "message", User: "U2", TS: pgAgo(29*time.Minute-time.Duration(i)*time.Second*6, i), Text: "noise"})
	}
	msgs = append(msgs, pgMsg{Type: "message", User: "U1", TS: pgAgo(time.Minute, 999), Text: "status update"})
	pgHistory(t, f, msgs)
	h := pgHub(t, f, 50)

	if !h.recentSelfDuplicate(context.Background(), "C1", "  status update ", 30) {
		t.Fatal("the operator's own post from a minute ago must count as a duplicate")
	}
}

// --------------------------------------------------------- find_decisions

func TestFindDecisions_SeesARecentDecisionInABusyChannel(t *testing.T) {
	f := newFakeSlack(t)
	msgs := pgBusyWeek(400, "we decided to ship on Friday")
	pgHistory(t, f, msgs)
	h := pgHub(t, f, 50)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{"channels": "alpha", "hours": 24})
	if isErr {
		t.Fatalf("unexpected error: %q", out)
	}
	tmWant(t, out, "we decided to ship on Friday")
}

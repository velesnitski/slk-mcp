package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ADR 125: the DM window reuses the unread sweep's pages, and the delta
// cursor never passes the moment the sweep began.

func countCalls(f *uaFakeSlack, method string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == method {
			n++
		}
	}
	return n
}

func TestRunUnreadSummary_DMWindowReusesTheUnreadSweepsPage(t *testing.T) {
	f := uaNewFakeSlack(t)
	recent := uaTS(-120, 100)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}))
	f.uaInfo(t, map[string]map[string]any{
		"D1": uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2", "last_read": recent}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D1": {uaMsg(recent, "U2", "agreed in the DM", nil)},
	})
	h := uaHub(t, f)
	p := uaUnreadDefaults()
	p.dmWindowHours = 4

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "agreed in the DM") {
		t.Fatalf("the DM window must still surface the read DM:\n%s", out)
	}
	if n := countCalls(f, "conversations.history"); n != 1 {
		t.Fatalf("conversations.history called %d times, want 1: the window reuses the unread page", n)
	}
}

func TestRunUnreadSummary_CursorNeverPassesTheSweepStart(t *testing.T) {
	// A message stamped after the sweep began stands for one that arrived
	// while the sweep was still reading: a cursor taken from it would let
	// the next pull skip anything that landed meanwhile in a channel
	// already read.
	f := uaNewFakeSlack(t)
	late := uaTS(+300, 500)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(late, "U2", "posted during the sweep", nil)},
	})
	h := uaHub(t, f)

	before := time.Now().Unix()
	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	if !strings.Contains(out, "posted during the sweep") {
		t.Fatalf("the message must still be shown:\n%s", out)
	}
	if strings.Contains(out, "cursor: "+late) {
		t.Fatalf("the cursor passed the sweep start:\n%s", out)
	}
	i := strings.Index(out, "cursor: ")
	if i < 0 {
		t.Fatalf("no cursor:\n%s", out)
	}
	cur, err := strconv.ParseFloat(strings.Fields(out[i+len("cursor: "):])[0], 64)
	if err != nil || int64(cur) < before || int64(cur) > time.Now().Unix() {
		t.Fatalf("cursor %v is not the sweep start (%v): %s", cur, before, out[i:])
	}
}

func TestCapCursor(t *testing.T) {
	start := time.Unix(1700000500, 0)
	for in, want := range map[string]string{
		"1700000400.000100": "1700000400.000100", // before the start: kept
		"1700000500.000000": "1700000500.000000", // at it: kept
		"1700000900.000100": "1700000500.000000", // after it: capped
		"":                  "",                  // nothing to place
		"garbage":           "garbage",           // unparseable: untouched
	} {
		if got := capCursor(in, start); got != want {
			t.Errorf("capCursor(%q) = %q, want %q", in, got, want)
		}
	}
	if got := capCursor("1700000900.000100", time.Time{}); got != "1700000900.000100" {
		t.Errorf("zero start must not cap: %q", got)
	}
}

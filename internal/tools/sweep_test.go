package tools

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 121: the sweep deadline, its notes, and the cursor rule.

// fastSweep scales SweepTimeoutSeconds steps to milliseconds for a test.
func fastSweep(t *testing.T) {
	t.Helper()
	old := sweepUnit
	sweepUnit = 80 * time.Millisecond
	t.Cleanup(func() { sweepUnit = old })
}

// stall is a fake-Slack handler for a call that never answers in time:
// it waits for the client to give up, then reports an error, as a
// request cut by the transport would.
func stall(r *http.Request) string {
	<-r.Context().Done()
	return `{"ok":false,"error":"request_timeout"}`
}

func withSweepTimeout(n int) func(*config.Config) {
	return func(c *config.Config) { c.SweepTimeoutSeconds = n }
}

// ---------------------------------------------------- get_unread_summary

func TestUnreadSummary_DeadlineKeepsTheIncomingCursorAndNamesTheSkippedPhases(t *testing.T) {
	fastSweep(t)
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	f.OnFunc("search.messages", stall) // the thread and own-thread backstops search
	h := uaHub(t, f, withSweepTimeout(1))
	p := uaUnreadDefaults()
	p.afterTS = uaTS(-3600, 0)

	start := time.Now()
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if time.Since(start) > 2*time.Second {
		t.Fatalf("the deadline did not bound the sweep (%v)", time.Since(start))
	}
	for _, want := range []string{
		"second thing", // what was gathered is still shown
		"incomplete: the get_unread_summary deadline passed",
		"thread-mentions",
		"cursor not advanced",
		"cursor: " + p.afterTS,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cursor: "+newest) {
		t.Fatalf("the cursor advanced past phases that never ran:\n%s", out)
	}
}

func TestUnreadSummary_NoDeadlineHitAdvancesTheCursorAsBefore(t *testing.T) {
	fastSweep(t)
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	h := uaHub(t, f, withSweepTimeout(50))

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	if !strings.Contains(out, "cursor: "+newest) || strings.Contains(out, "incomplete") {
		t.Fatalf("an unhurried sweep must look exactly as before:\n%s", out)
	}
}

func TestUnreadSummary_EveryWorkspaceCutByTheDeadlineKeepsItsCursor(t *testing.T) {
	// Workspaces run concurrently (ADR 122), so a deadline cuts each of
	// them mid-sweep rather than skipping the later ones; each must still
	// return its own incoming cursor in the combined token.
	fastSweep(t)
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	f.OnFunc("search.messages", stall)
	h := uaMultiHub(t, f)
	h.cfg.SweepTimeoutSeconds = 1
	p := uaUnreadDefaults()
	// Computed once: uaTS is relative to now, so a second call could land
	// in the next second and compare against a different value.
	primaryAfter, secondaryAfter := uaTS(-3600, 0), uaTS(-1800, 0)
	p.afterTS = "primary=" + primaryAfter + ";secondary=" + secondaryAfter

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if strings.Count(out, "incomplete: the get_unread_summary deadline passed") != 2 {
		t.Fatalf("both workspaces must report the cut:\n%s", out)
	}
	if !strings.Contains(out, "cursor: primary="+primaryAfter+";secondary="+secondaryAfter) {
		t.Fatalf("both workspaces must keep their incoming cursors:\n%s", out)
	}
}

func TestUnreadSummary_ZeroTimeoutMeansNoDeadline(t *testing.T) {
	ctx, cancel := sweepContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("SweepTimeoutSeconds=0 must not set a deadline")
	}
	ctx, cancel = sweepContext(context.Background(), 3)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a positive SweepTimeoutSeconds must set a deadline")
	}
}

// --------------------------------------------------------- get_mentions

func TestMentions_DeadlineReturnsWhatWasFoundAndSaysTheRestIsMissing(t *testing.T) {
	fastSweep(t)
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	hit := uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{
		"matches": []map[string]any{uaHit("D1", "U2", uaTS(-600, 100), "U2", "can you approve the invoice?", "")},
		"total":   1,
	}})
	f.OnFunc("search.messages", func(r *http.Request) string {
		if strings.Contains(r.Form.Get("query"), "to:me") {
			return hit
		}
		return stall(r)
	})
	h := uaHub(t, f, withSweepTimeout(1))

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	for _, want := range []string{"approve the invoice", "incomplete: the get_mentions deadline passed", "search-2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cursor not advanced") {
		t.Fatalf("get_mentions has no cursor to talk about:\n%s", out)
	}
}

func TestMentions_ACutSweepWithNoHitsIsNotReportedAsNoMentions(t *testing.T) {
	fastSweep(t)
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.OnFunc("search.messages", stall)
	h := uaHub(t, f, withSweepTimeout(1))

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	if !strings.Contains(out, "incomplete") {
		t.Fatalf("an empty list from a cut sweep must say it is incomplete:\n%s", out)
	}
	if strings.Contains(out, mentionsEmptyMsg(false, 72)) {
		t.Fatalf("a cut sweep claimed there were no mentions:\n%s", out)
	}
}

func TestMentions_EveryWorkspaceCutByTheDeadlineSaysSo(t *testing.T) {
	fastSweep(t)
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.OnFunc("search.messages", stall)
	h := uaMultiHub(t, f)
	h.cfg.SweepTimeoutSeconds = 1

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	if strings.Count(out, "incomplete: the get_mentions deadline passed") != 2 {
		t.Fatalf("both workspaces must report the cut:\n%s", out)
	}
}

// ------------------------------------------------------------ the tracker

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestSweep_APhaseAfterTheDeadlineIsNotRunAndIsRecorded(t *testing.T) {
	sw := newSweep(cancelledCtx(), uaLog(), "get_mentions", "w")
	ran := false
	err := sw.run("p1", func(context.Context) error { ran = true; return nil })
	if ran || err == nil || !sw.incomplete() || sw.cut[0] != "p1" {
		t.Fatalf("ran=%v err=%v cut=%v", ran, err, sw.cut)
	}
}

func TestSweep_APhasesOwnErrorIsNotADeadlineCut(t *testing.T) {
	sw := newSweep(context.Background(), uaLog(), "t", "w")
	boom := errors.New("boom")
	if err := sw.run("p", func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if sw.incomplete() || sw.note(true) != "" {
		t.Fatalf("an ordinary failure must not be reported as a deadline cut: %v", sw.cut)
	}
}

func TestSweep_DisplayPhasesNeverHoldTheCursor(t *testing.T) {
	sw := newSweep(cancelledCtx(), uaLog(), "get_unread_summary", "w")
	_ = sw.runDisplay("answered-dms", func(context.Context) error { return nil })
	if sw.incomplete() {
		t.Fatal("a display-only phase must not make the sweep incomplete")
	}
	note := sw.note(true)
	if !strings.Contains(note, "answered-dms") || !strings.Contains(note, "less filtered") || strings.Contains(note, "cursor") {
		t.Fatalf("note = %q", note)
	}
}

func TestSweep_EveryPhaseIsLoggedWithItsDuration(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	sw := newSweep(context.Background(), log, "get_unread_summary", "primary")
	_ = sw.run("unread", func(context.Context) error { return nil })
	_ = sw.run("priority", func(context.Context) error { return errors.New("nope") })

	got := buf.String()
	for _, want := range []string{"sweep phase", "phase=unread", "phase=priority", "workspace=primary", "took=", "err=nope"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %q:\n%s", want, got)
		}
	}
}

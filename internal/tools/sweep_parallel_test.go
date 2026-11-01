package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ADR 131: the backstop and canvas phases run beside the unread pass, so a
// sweep takes about as long as its slowest phase, not the sum of them.
func TestUnreadSweep_PhasesRunConcurrently(t *testing.T) {
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	const delay = 200 * time.Millisecond
	f.OnFunc("search.messages", func(*http.Request) string {
		time.Sleep(delay)
		return `{"ok":true,"messages":{"matches":[],"total":0}}`
	})
	f.OnFunc("files.list", func(*http.Request) string {
		time.Sleep(delay)
		return `{"ok":true,"files":[]}`
	})
	h := uaHub(t, f)
	p := uaUnreadDefaults()
	p.showTimings = true

	start := time.Now()
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))
	took := time.Since(start)

	// thread-mentions (two searches), own-threads (one search) and canvases
	// each wait at least `delay`: run one after another that is >= 4×delay.
	t.Logf("sweep took %s", took)
	if took >= 3*delay {
		t.Fatalf("sweep took %s; the slow phases ran one after another\n%s", took, out)
	}
	if !strings.Contains(out, "timings: ") || !strings.Contains(out, "own-threads ") || !strings.Contains(out, "canvases ") {
		t.Fatalf("show_timings must list the phases:\n%s", out)
	}
	if newest == "" {
		t.Fatal("fixture")
	}
}

package tools

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 122: workspaces sweep concurrently; SLACK_SWEEP_EXCLUDE.

func TestUnreadSummary_WorkspacesAreSweptConcurrently(t *testing.T) {
	// The first users.conversations call waits for the second. If the
	// workspaces ran one after another the second call could never
	// arrive while the first waits, and the barrier would time out.
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	var arrived int32
	var waiting, overlapped atomic.Bool
	both := make(chan struct{})
	var once sync.Once
	list := `{"ok":true,"channels":[],"response_metadata":{"next_cursor":""}}`
	f.OnFunc("users.conversations", func(r *http.Request) string {
		if atomic.AddInt32(&arrived, 1) >= 2 {
			// Overlap only counts while the first call is still held:
			// a sequential sweep would get here after it timed out.
			if waiting.Load() {
				overlapped.Store(true)
			}
			once.Do(func() { close(both) })
			return list
		}
		waiting.Store(true)
		defer waiting.Store(false)
		select {
		case <-both:
		case <-time.After(2 * time.Second):
		}
		return list
	})
	h := uaMultiHub(t, f)

	_ = resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	if !overlapped.Load() {
		t.Fatal("the two workspaces were swept one after another")
	}
}

func TestUnreadSummary_ResultsKeepRegistryOrder(t *testing.T) {
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	h := uaMultiHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	if i, j := strings.Index(out, "## [primary]"), strings.Index(out, "## [secondary]"); i < 0 || j < i {
		t.Fatalf("primary must render before secondary:\n%s", out)
	}
}

func TestUnreadSummary_ExcludedWorkspaceIsSkippedNamedAndKeepsItsCursor(t *testing.T) {
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	h := uaMultiHub(t, f)
	h.cfg.SweepExclude = []string{"Secondary"}
	p := uaUnreadDefaults()
	secondaryAfter := uaTS(-1800, 0)
	p.afterTS = "secondary=" + secondaryAfter

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if strings.Contains(out, "## [secondary]") {
		t.Fatalf("an excluded workspace must not be swept:\n%s", out)
	}
	if !strings.Contains(out, "not swept by default (SLACK_SWEEP_EXCLUDE): secondary") {
		t.Fatalf("the exclusion must be named, not look like 'caught up':\n%s", out)
	}
	if !strings.Contains(out, "secondary="+secondaryAfter) {
		t.Fatalf("the excluded workspace's cursor must carry over:\n%s", out)
	}
	if !strings.Contains(out, "## [primary]") {
		t.Fatalf("the remaining workspace keeps its heading:\n%s", out)
	}
}

func TestUnreadSummary_NamingAnExcludedWorkspaceStillReadsIt(t *testing.T) {
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	h := uaMultiHub(t, f)
	h.cfg.SweepExclude = []string{"secondary"}

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "secondary"))

	if strings.Contains(out, "not swept by default") || !strings.Contains(out, "second thing") {
		t.Fatalf("an explicit workspace must be read:\n%s", out)
	}
}

func TestMentions_ExcludedWorkspaceIsNamed(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaNoSearch()
	h := uaMultiHub(t, f)
	h.cfg.SweepExclude = []string{"secondary"}

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	if strings.Contains(out, "## [secondary]") || !strings.Contains(out, "not swept by default (SLACK_SWEEP_EXCLUDE): secondary") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestSweepTargets(t *testing.T) {
	h := &Hub{registry: []slack.Workspace{{Name: "primary"}, {Name: "secondary"}, {Name: "third"}}}
	h.cfg = &config.Config{}

	if read, ex := h.sweepTargets(""); len(read) != 3 || ex != nil {
		t.Fatalf("no exclusion configured: %v %v", workspaceOrder(read), ex)
	}
	h.cfg.SweepExclude = []string{" THIRD ", "nope"}
	read, ex := h.sweepTargets("")
	if strings.Join(workspaceOrder(read), ",") != "primary,secondary" || strings.Join(workspaceOrder(ex), ",") != "third" {
		t.Fatalf("read=%v excluded=%v", workspaceOrder(read), workspaceOrder(ex))
	}
	if read, ex := h.sweepTargets("third"); len(read) != 1 || ex != nil {
		t.Fatal("a named workspace ignores the exclusion")
	}
	if read, _ := h.sweepTargets("ghost"); read != nil {
		t.Fatal("unknown workspace must be nil")
	}
	h.cfg.SweepExclude = []string{"primary", "secondary", "third"}
	if read, ex := h.sweepTargets(""); len(read) != 3 || ex != nil {
		t.Fatal("excluding everything must be ignored rather than sweep nothing")
	}
}

func TestExcludedNote(t *testing.T) {
	if excludedNote(nil) != "" {
		t.Fatal("no note without exclusions")
	}
	got := excludedNote([]slack.Workspace{{Name: "b"}, {Name: "c"}})
	if got != "not swept by default (SLACK_SWEEP_EXCLUDE): b, c — pass workspace=b to read it" {
		t.Fatalf("got %q", got)
	}
}

func TestFanOut_KeepsOrderAndRunsAll(t *testing.T) {
	ws := []slack.Workspace{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got := fanOut(ws, func(w slack.Workspace) string {
		if w.Name == "a" {
			time.Sleep(20 * time.Millisecond) // finishes last, must still come first
		}
		return w.Name
	})
	if strings.Join(got, "") != "abc" {
		t.Fatalf("got %v", got)
	}
}

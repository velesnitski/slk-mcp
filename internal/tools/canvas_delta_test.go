package tools

import (
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

func TestCanvasMentionsSelf(t *testing.T) {
	if !canvasMentionsSelf("owner: <@U123> please review", "U123") {
		t.Fatal("want the <@Uxxx> form detected")
	}
	// The markdown export drops the angle brackets on some canvases.
	if !canvasMentionsSelf("assigned to @U123", "U123") {
		t.Fatal("want the bare @Uxxx form detected")
	}
	if canvasMentionsSelf("owner: <@U999>", "U123") {
		t.Fatal("must not match a different user")
	}
	if canvasMentionsSelf("anything", "") {
		t.Fatal("empty selfID must never claim a mention")
	}
}

func TestHumanSince(t *testing.T) {
	cases := []struct {
		sec  int64
		want string
	}{
		{-5, "just now"},
		{30, "just now"},
		{600, "10m ago"},
		{7200, "2h ago"},
		{172800, "2d ago"},
	}
	for _, c := range cases {
		if got := humanSince(c.sec); got != c.want {
			t.Fatalf("humanSince(%d) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestCanvasSince_CursorWinsOverHours(t *testing.T) {
	const now = 1_800_000_000
	// A delta cursor pins canvases to the same window as messages.
	if got := canvasSince("1799999000.000200", 24, now); got != 1799999000 {
		t.Fatalf("want cursor seconds, got %d", got)
	}
	// No cursor: fall back to the hours window.
	if got := canvasSince("", 2, now); got != now-7200 {
		t.Fatalf("want now-2h, got %d", got)
	}
	// A non-numeric cursor must not be read as a timestamp.
	if got := canvasSince("primary=1;secondary=2", 1, now); got != now-3600 {
		t.Fatalf("want fallback for an unparsable cursor, got %d", got)
	}
	if got := canvasSince("", 0, now); got != 0 {
		t.Fatalf("want 0 (disabled), got %d", got)
	}
}

func TestCanvasChannelLabels_FallsBackToID(t *testing.T) {
	got := canvasChannelLabels([]string{"C1", "C2"}, map[string]string{"C1": "team-updates"})
	if len(got) != 2 || got[0] != "#team-updates" || got[1] != "C2" {
		t.Fatalf("want name then raw-ID fallback, got %v", got)
	}
}

func TestRenderCanvasDelta_MentionsFirst(t *testing.T) {
	const now = 2000
	hits := []canvasHit{
		{Ref: slack.CanvasRef{ID: "F1", Title: "Newest", Created: 100, Updated: 1990}, Probed: true},
		{Ref: slack.CanvasRef{ID: "F2", Title: "Tagged", Created: 100, Updated: 1400,
			Permalink: "https://example.slack.com/docs/F2"}, Probed: true, MentionsYou: true,
			Labels: []string{"#team-updates"}},
	}
	got := renderCanvasDelta(hits, now)

	if !strings.HasPrefix(got, "CANVASES — 2 updated, 1 mentioning you\n") {
		t.Fatalf("header wrong: %q", got)
	}
	// The tagged canvas outranks the newer untagged one — a mention is the
	// only reason this section exists.
	iTagged := strings.Index(got, "Tagged")
	iNewest := strings.Index(got, "Newest")
	if iTagged < 0 || iNewest < 0 || iTagged > iNewest {
		t.Fatalf("want the @you canvas first:\n%s", got)
	}
	if !strings.Contains(got, "@you — Tagged — #team-updates — edited 10m ago") {
		t.Fatalf("tagged line wrong:\n%s", got)
	}
	if !strings.Contains(got, "https://example.slack.com/docs/F2") {
		t.Fatalf("want permalink emitted:\n%s", got)
	}
}

func TestRenderCanvasDelta_CreatedVsEditedAndUnprobed(t *testing.T) {
	const now = 5000
	hits := []canvasHit{
		{Ref: slack.CanvasRef{ID: "F1", Title: "Fresh", Created: 4990, Updated: 4990}},
	}
	got := renderCanvasDelta(hits, now)
	if !strings.Contains(got, "Fresh — created just now") {
		t.Fatalf("want 'created' for an unedited canvas:\n%s", got)
	}
	// Unprobed must say so rather than imply "no mention found".
	if !strings.Contains(got, "body not checked") {
		t.Fatalf("want the unprobed caveat:\n%s", got)
	}
}

func TestRenderCanvasDelta_EmptyIsEmpty(t *testing.T) {
	if renderCanvasDelta(nil, 0) != "" {
		t.Fatal("no hits must render nothing, not an empty header")
	}
}

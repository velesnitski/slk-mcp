package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestCanvasToText_HTMLReducedToText(t *testing.T) {
	raw := []byte(`<html><head><style>.x{color:red}</style></head><body>` +
		`<h1>Runbook</h1><p>Step one &amp; done.</p>` +
		`<ul><li>alpha</li><li>beta</li></ul>` +
		`<script>alert(1)</script></body></html>`)
	got, trunc := canvasToText(raw, "text/html", canvasMaxBytes)
	if trunc {
		t.Fatal("should not truncate a small canvas")
	}
	for _, want := range []string{"Runbook", "Step one & done.", "• alpha", "• beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// script/style contents must be dropped, and no stray tags survive.
	if strings.Contains(got, "alert") || strings.Contains(got, "color:red") || strings.Contains(got, "<") {
		t.Errorf("script/style/tags leaked:\n%s", got)
	}
}

func TestCanvasToText_MarkdownPassthrough(t *testing.T) {
	raw := []byte("# Title\n\n- one\n- two\n\nplain text, no tags")
	got, _ := canvasToText(raw, "text/markdown", canvasMaxBytes)
	if got != "# Title\n\n- one\n- two\n\nplain text, no tags" {
		t.Fatalf("markdown must pass through untouched; got:\n%q", got)
	}
}

func TestCanvasToText_TruncatesAtCap(t *testing.T) {
	raw := []byte(strings.Repeat("a", 100))
	got, trunc := canvasToText(raw, "text/plain", 10)
	if !trunc || len(got) != 10 {
		t.Fatalf("want truncated 10-char body; got trunc=%v len=%d", trunc, len(got))
	}
}

func TestCanvasToText_CollapsesBlankRuns(t *testing.T) {
	got, _ := canvasToText([]byte("a\n\n\n\n\nb"), "text/plain", canvasMaxBytes)
	if got != "a\n\nb" {
		t.Fatalf("blank runs should collapse to one paragraph break; got %q", got)
	}
}

func TestPickNewestCanvas(t *testing.T) {
	if pickNewestCanvas(nil) != nil {
		t.Fatal("empty list → nil")
	}
	mk := func(id string, created int64) goslack.File {
		f := goslack.File{}
		f.ID = id
		f.Created = goslack.JSONTime(created)
		return f
	}
	files := []goslack.File{mk("F1", 100), mk("F3", 300), mk("F2", 200)}
	got := pickNewestCanvas(files)
	if got == nil || got.ID != "F3" {
		t.Fatalf("want newest F3; got %+v", got)
	}
}

func TestCanvasDateVariants(t *testing.T) {
	vs, err := canvasDateVariants("2026-07-23")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"23.07.2026", "23.07.26", "2026-07-23", "23.07", "23/07/2026", "23.7.2026", "23.7"} {
		found := false
		for _, v := range vs {
			if v == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing variant %q in %v", want, vs)
		}
	}
	if vs, _ := canvasDateVariants(""); vs != nil {
		t.Error("empty date → nil variants")
	}
	if _, err := canvasDateVariants("23.07.2026"); err == nil {
		t.Error("non-ISO input must error")
	}
}

func TestSelectCanvas_DateAndMatch(t *testing.T) {
	mkTitled := func(id, title string, created int64) goslack.File {
		f := goslack.File{}
		f.ID = id
		f.Title = title
		f.Created = goslack.JSONTime(created)
		return f
	}
	files := []goslack.File{
		mkTitled("F1", "22.07.2026 Tech Meet", 100),
		mkTitled("F2", "23.07.2026 Tech Meet", 200),
		mkTitled("F3", "Runbook", 300),
		mkTitled("F4", "23.07.2026 Retro", 150),
	}
	vs, _ := canvasDateVariants("2026-07-23")

	// date only → newest among the two 23.07 canvases.
	if got := selectCanvas(files, "", vs); got == nil || got.ID != "F2" {
		t.Fatalf("date-only: want F2, got %+v", got)
	}
	// date + match → the Retro one.
	if got := selectCanvas(files, "retro", vs); got == nil || got.ID != "F4" {
		t.Fatalf("date+match: want F4, got %+v", got)
	}
	// match only, case-insensitive.
	if got := selectCanvas(files, "runbook", nil); got == nil || got.ID != "F3" {
		t.Fatalf("match-only: want F3, got %+v", got)
	}
	// miss → nil.
	if got := selectCanvas(files, "standup", vs); got != nil {
		t.Fatalf("miss must be nil, got %+v", got)
	}
}

func TestRenderCanvasList_NewestFirstUntitled(t *testing.T) {
	a := goslack.File{}
	a.Title = "Old"
	a.Created = goslack.JSONTime(100)
	b := goslack.File{}
	b.Created = goslack.JSONTime(200) // untitled, newer
	got := renderCanvasList([]goslack.File{a, b})
	if !strings.HasPrefix(got, "- (untitled)") || !strings.Contains(got, "- Old") {
		t.Fatalf("want untitled first then Old; got:\n%s", got)
	}
}

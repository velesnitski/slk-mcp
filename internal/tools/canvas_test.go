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

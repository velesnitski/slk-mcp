package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ADR 118: fuzz targets for every parser fed by Slack attachments. Any
// colleague can attach a file, so these inputs are untrusted: the
// contract is no panic, no runaway work, and outputs that keep their
// invariants. CI runs the seed corpus; `go test -fuzz` explores further.

func FuzzExtractPDF(f *testing.F) {
	f.Add([]byte(dcPDF("Quarterly summary for the team", "Second line")))
	f.Add(pdfDeck(false))
	f.Add(pdfDeck(true))
	f.Add([]byte("%PDF-1.5\n1 0 obj << /Type /Catalog /Pages 1 0 R >> endobj trailer << /Root 1 0 R >>"))
	f.Add([]byte("stream\nendstream"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, cm := extractPDFTextMode(raw)
		pages, total := pdfImageOnlyPages(raw, cm)
		if total > pdfMaxPages {
			t.Fatalf("page walk exceeded its bound: %d", total)
		}
		for _, p := range pages {
			if p < 1 || p > total {
				t.Fatalf("flagged page %d outside 1..%d", p, total)
			}
		}
	})
}

func FuzzHTMLToText(f *testing.F) {
	f.Add(`<html><body><p>Hello</p><script>var a="</style>";x()</script></body></html>`)
	f.Add(`<script type="application/json">{"a":1}</script><div data-x="` + strings.Repeat("QUJD", 60) + `"></div>`)
	f.Add(`<!-- c --><style>p{}</style>&amp;&lt;`)
	f.Add(`<script>` + strings.Repeat("x=1;", 6000) + `</script>`)
	f.Fuzz(func(t *testing.T, s string) {
		out := htmlToText(s)
		if utf8.ValidString(s) && !utf8.ValidString(out) {
			t.Fatalf("valid UTF-8 in, invalid out")
		}
		if strings.Contains(strings.ToLower(out), "<script") && !strings.Contains(strings.ToLower(s), "<script") {
			t.Fatalf("a script tag appeared from nowhere")
		}
	})
}

func FuzzDocumentToText(f *testing.F) {
	f.Add("plain text\r\nline two", "text/plain")
	f.Add("<!doctype html><p>x</p>", "text/plain")
	f.Fuzz(func(t *testing.T, body, mimetype string) {
		_ = documentToText(body, mimetype)
	})
}

func FuzzXLSXToText(f *testing.F) {
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = xlsxToText(raw)
	})
}

func FuzzIsClosingAckText(f *testing.F) {
	for _, s := range []string{"Да", "спасибо!", ":+1:", "Да, но когда релиз?", `named the conversation "x"`, ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if isClosingAckText(s) && strings.ContainsAny(s, "?？") && len(strings.Fields(s)) == 2 {
			t.Fatalf("a two-word message with a question was dropped as a closer: %q", s)
		}
	})
}

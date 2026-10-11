package tools

import (
	"strings"
	"testing"
)

// ADR 118: branches of the PDF reader that no fixture exercised —
// found as uncovered mutants. Each one is a real encoding a producer
// may use, and getting it wrong garbles or drops document text.

func TestPDFLiteralString_Escapes(t *testing.T) {
	cases := map[string]string{
		`(line\nbreak)`:       "line\nbreak",
		`(tab\there)`:         "tab\there",
		`(cr\rlf)`:            "cr\rlf",
		`(bs\bff\f)`:          "bs\bff\f",
		`(octal \101\102C)`:   "octal ABC",
		`(short \7z)`:         "short \x07z",
		"(join\\\nlines)":     "joinlines",
		"(join\\\r\nlines)":   "joinlines",
		`(nested (paren) ok)`: "nested (paren) ok",
		`(escaped \( paren)`:  "escaped ( paren",
		`(unterminated`:       "unterminated",
	}
	for in, want := range cases {
		got, next := pdfLiteralString([]byte(in), 0)
		if string(got) != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
		if next > len(in) {
			t.Errorf("%q: next %d past the input", in, next)
		}
	}
}

func TestPDFContentText_HexStringsAndLineOperators(t *testing.T) {
	content := []byte("BT 1 0 0 1 72 700 Tm <48656C6C6F> Tj 0 -20 Td (world) Tj T* (third) Tj 1 0 0 1 72 600 Tm (fourth) Tj ET")

	got := pdfTidy(pdfContentText(content, nil))

	lines := strings.Split(got, "\n")
	want := []string{"Hello", "world", "third", "fourth"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %v", len(lines), got, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d: got %q, want %q (all: %q)", i, lines[i], want[i], got)
		}
	}
}

func TestPDFContentText_SameLineStaysJoined(t *testing.T) {
	got := pdfTidy(pdfContentText([]byte("BT 1 0 0 1 72 700 Tm (one ) Tj (two) Tj ET"), nil))
	if got != "one two" {
		t.Fatalf("text on one baseline must not be split: %q", got)
	}
}

func TestPDFHexValue_RejectsGarbage(t *testing.T) {
	if v, n := pdfHexValue("zz"); v != 0 || n != 1 {
		t.Fatalf("got %d,%d", v, n)
	}
	if v, n := pdfHexValue("0041"); v != 0x41 || n != 2 {
		t.Fatalf("got %d,%d", v, n)
	}
}

// pdfPagesVariant: /Contents given as an array, as a reference to an
// array object, and /Resources given by reference with an XObject table
// that is itself a reference.
func pdfPagesVariant() []byte {
	prose := "Quarterly results were strong across every channel and region this period"
	return buildPDF([]pdfObj{
		{num: 1, dict: "<< /Type /Catalog /Pages 2 0 R >>"},
		{num: 2, dict: "<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>"},
		{num: 3, dict: "<< /Type /Page /Parent 2 0 R /Contents [10 0 R 11 0 R] >>"},
		{num: 4, dict: "<< /Type /Page /Parent 2 0 R /Contents 30 0 R >>"},
		{num: 5, dict: "<< /Type /Page /Parent 2 0 R /Resources 40 0 R /Contents 12 0 R >>"},
		{num: 10, dict: "<< /Length 9 >>", stream: textOps("Quarterly results were strong")},
		{num: 11, dict: "<< /Length 9 >>", stream: textOps("across every channel and region this period")},
		{num: 12, dict: "<< /Length 9 >>", stream: "q /Im1 Do Q"},
		{num: 13, dict: "<< /Length 9 >>", stream: textOps(prose)},
		{num: 30, dict: "[13 0 R]"},
		{num: 40, dict: "<< /XObject 41 0 R >>"},
		{num: 41, dict: "<< /Im1 42 0 R >>"},
		{num: 42, dict: "<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 3 >>", stream: "abc"},
	}, 1, false)
}

func TestPDFImageOnlyPages_ContentsArraysAndResourceReferences(t *testing.T) {
	raw := pdfPagesVariant()
	_, _, cm := extractPDFTextMode(raw)

	pages, total := pdfImageOnlyPages(raw, cm)

	if total != 3 {
		t.Fatalf("page count %d, want 3", total)
	}
	if len(pages) != 1 || pages[0] != 3 {
		t.Fatalf("flagged %v, want [3]: pages 1 and 2 carry text via a contents array and an array object", pages)
	}
	o := indexPDFObjects(raw)
	if refs := o.xobjectRefs(o.dict[5]); len(refs) != 1 || refs[0] != 42 {
		t.Fatalf("xobject refs through /Resources and /XObject references: %v", refs)
	}
}

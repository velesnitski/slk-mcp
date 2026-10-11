package tools

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"strings"
	"testing"
)

// ADR 117: pages that are pictures of content must be named, because
// the extracted text carries no trace of them.

type pdfObj struct {
	num    int
	dict   string
	stream string
}

// buildPDF writes objects plainly, or — with objStm — puts every
// non-stream object into one compressed /ObjStm, as PDF 1.5+ exporters do.
func buildPDF(objs []pdfObj, root int, objStm bool) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n")
	var packed []pdfObj
	for _, o := range objs {
		if objStm && o.stream == "" {
			packed = append(packed, o)
			continue
		}
		fmt.Fprintf(&b, "%d 0 obj\n%s\n", o.num, o.dict)
		if o.stream != "" {
			fmt.Fprintf(&b, "stream\n%s\nendstream\n", o.stream)
		}
		b.WriteString("endobj\n")
	}
	if len(packed) > 0 {
		var head, body strings.Builder
		for _, o := range packed {
			fmt.Fprintf(&head, "%d %d ", o.num, body.Len())
			body.WriteString(o.dict + "\n")
		}
		raw := head.String() + body.String()
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write([]byte(raw))
		zw.Close()
		fmt.Fprintf(&b, "99 0 obj\n<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
			len(packed), len(head.String()), z.Len())
		b.Write(z.Bytes())
		b.WriteString("\nendstream\nendobj\n")
	}
	fmt.Fprintf(&b, "trailer\n<< /Root %d 0 R >>\n%%%%EOF\n", root)
	return b.Bytes()
}

func textOps(s string) string { return "BT /F1 12 Tf 72 700 Td (" + s + ") Tj ET" }

// pdfDeck: page 1 prose, page 2 a picture with a short title, page 3
// text that lives inside a form XObject the page draws.
func pdfDeck(objStm bool) []byte {
	prose := "Quarterly results were strong across every channel and region this period"
	objs := []pdfObj{
		{num: 1, dict: "<< /Type /Catalog /Pages 2 0 R >>"},
		{num: 2, dict: "<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>"},
		{num: 3, dict: "<< /Type /Page /Parent 2 0 R /Contents 10 0 R >>"},
		{num: 4, dict: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 20 0 R >> >> /Contents 11 0 R >>"},
		{num: 5, dict: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm1 21 0 R >> >> /Contents 12 0 R >>"},
		{num: 10, dict: "<< /Length 99 >>", stream: textOps(prose)},
		{num: 11, dict: "<< /Length 99 >>", stream: textOps("Q3 results") + " q 900 0 0 500 0 0 cm /Im1 Do Q"},
		{num: 12, dict: "<< /Length 99 >>", stream: "q /Fm1 Do Q"},
		{num: 20, dict: "<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 3 >>", stream: "abc"},
		{num: 21, dict: "<< /Type /XObject /Subtype /Form /Length 99 >>", stream: textOps(prose)},
	}
	return buildPDF(objs, 1, objStm)
}

func TestPDFImageOnlyPages_NamesThePicturePageOnly(t *testing.T) {
	for _, objStm := range []bool{false, true} {
		raw := pdfDeck(objStm)
		_, _, cm := extractPDFTextMode(raw)
		pages, total := pdfImageOnlyPages(raw, cm)
		if total != 3 {
			t.Fatalf("objStm=%v: page count %d, want 3", objStm, total)
		}
		if len(pages) != 1 || pages[0] != 2 {
			t.Fatalf("objStm=%v: flagged %v, want [2] — the form-text page must not count as a picture", objStm, pages)
		}
	}
}

func TestPDFImageOnlyPages_NoPageTreeSaysNothing(t *testing.T) {
	// The single-stream fixture the extractor tests use has no page tree;
	// guessing would be worse than silence.
	if pages, total := pdfImageOnlyPages([]byte(dcPDF("Some text here for the reader")), nil); pages != nil || total != 0 {
		t.Fatalf("got %v/%d, want nothing", pages, total)
	}
}

func TestPDFPageRanges(t *testing.T) {
	if got := pdfPageRanges([]int{42, 35, 36, 37, 44, 45, 38, 39, 40}); got != "35–40, 42, 44–45" {
		t.Fatalf("got %q", got)
	}
}

func TestReadDocument_PicturePagesAreNamedAndTheFileKept(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "deck.pdf", "application/pdf", string(pdfDeck(true)))))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	tmWant(t, got, "3 pages", "NOT IN THE TEXT: 1 page(s)", ": 2. Read them visually; file kept at: ")
	path := strings.TrimSpace(got[strings.Index(got, "file kept at: ")+len("file kept at: "):])
	path = strings.SplitN(path, "\n", 2)[0]
	defer os.Remove(path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file must stay so the pictured pages can be opened: %v", err)
	}
}

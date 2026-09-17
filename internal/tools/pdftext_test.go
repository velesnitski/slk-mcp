package tools

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// The extractor looks for stream/endstream pairs, not a valid xref
// table, so a fixture needs only that much scaffolding.
func minimalPDF(content string) []byte {
	return []byte("%PDF-1.4\n1 0 obj\n<< /Length " + fmt.Sprint(len(content)) +
		" >>\nstream\n" + content + "\nendstream\nendobj\n%%EOF\n")
}

func twoStreamPDF(first, second string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n1 0 obj\n<< >>\nstream\n")
	out.WriteString(first)
	out.WriteString("\nendstream\nendobj\n2 0 obj\n<< >>\nstream\n")
	out.WriteString(second)
	out.WriteString("\nendstream\nendobj\n%%EOF\n")
	return out.Bytes()
}

func flatePDF(content string) []byte {
	var deflated bytes.Buffer
	zw := zlib.NewWriter(&deflated)
	if _, err := zw.Write([]byte(content)); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n1 0 obj\n<< /Filter /FlateDecode >>\nstream\n")
	out.Write(deflated.Bytes())
	out.WriteString("\nendstream\nendobj\n%%EOF\n")
	return out.Bytes()
}

const sampleContent = "BT\n(The quick brown fox jumps over the lazy dog) Tj\n" +
	"0 -14 Td\n(Second line of the very same document body) Tj\nET\n"

func TestExtractPDFText_uncompressedStream(t *testing.T) {
	text, ok := extractPDFText(minimalPDF(sampleContent))
	if !ok {
		t.Fatal("an uncompressed content stream must extract")
	}
	if !strings.Contains(text, "quick brown fox") {
		t.Errorf("first line missing: %q", text)
	}
	if !strings.Contains(text, "Second line of the very same") {
		t.Errorf("second line missing: %q", text)
	}
	if strings.Contains(text, "dogSecond") {
		t.Errorf("Td must break the line: %q", text)
	}
}

func TestExtractPDFText_flateStream(t *testing.T) {
	text, ok := extractPDFText(flatePDF(sampleContent))
	if !ok {
		t.Fatal("a FlateDecode content stream must extract")
	}
	if !strings.Contains(text, "quick brown fox") {
		t.Errorf("deflated text missing: %q", text)
	}
}

// The case that matters most in practice: a subset-embedded font, where
// the content stream holds glyph indices private to that font and the
// only honest way back to characters is the document's own /ToUnicode
// table. Reading these bytes directly yields noise, not text.
func TestExtractPDFText_cidFontUsesToUnicode(t *testing.T) {
	cmap := "/CIDInit /ProcSet findresource begin\nbegincmap\n" +
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
		"1 beginbfchar\n<0003> <0020>\nendbfchar\n" +
		"1 beginbfrange\n<0024> <003D> <0041>\nendbfrange\nendcmap\n"

	var content strings.Builder
	content.WriteString("BT\n<")
	for rep := 0; rep < 3; rep++ {
		for i := 0; i < 20; i++ { // 0x24+i maps to 'A'+i
			fmt.Fprintf(&content, "%04X", 0x24+i)
		}
		fmt.Fprintf(&content, "%04X", 0x0003) // space
	}
	content.WriteString("> Tj\nET\n")

	text, ok := extractPDFText(twoStreamPDF(cmap, content.String()))
	if !ok {
		t.Fatal("a CID font with a ToUnicode table must extract")
	}
	if !strings.Contains(text, "ABCDEFGHIJKLMNOPQRST") {
		t.Errorf("glyph indices were not mapped through the CMap: %q", text)
	}
	// The CMap stream itself is not page content and must not be echoed.
	if strings.Contains(text, "beginbfrange") {
		t.Errorf("the CMap stream leaked into the text: %q", text)
	}
}

func TestPDFBuildToUnicode_parsesBothRangeForms(t *testing.T) {
	// Array form gives one destination per code; triple form walks the
	// range. Parsing the array as a triple would map the wrong codes.
	cmap := "begincmap\n2 beginbfrange\n" +
		"<0010> <0012> [<0058> <0059> <005A>]\n" +
		"<0020> <0022> <0061>\n" +
		"endbfrange\nendcmap\n"
	cm := pdfBuildToUnicode([][]byte{[]byte(cmap)})
	if cm == nil {
		t.Fatal("expected a CMap")
	}
	for code, want := range map[uint32]string{
		0x10: "X", 0x11: "Y", 0x12: "Z",
		0x20: "a", 0x21: "b", 0x22: "c",
	} {
		if got := cm.m[code]; got != want {
			t.Errorf("code %#04x = %q, want %q", code, got, want)
		}
	}
}

// A scan has no text layer: the stream neither inflates nor looks like
// content, and a page of noise would be worse than handing back a file.
func TestExtractPDFText_noTextLayerIsRejected(t *testing.T) {
	if _, ok := extractPDFText(minimalPDF("\x89PNG\x0d\x0a\x1a\x0a\x00\x00\x00binaryimagedata")); ok {
		t.Fatal("a stream with no text operators must not report success")
	}
}

// Glyph indices with no CMap to resolve them must fail the gate rather
// than render as noise.
func TestExtractPDFText_glyphNoiseIsRejected(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("<0003000400050006> Tj\n")
	}
	if text, ok := extractPDFText(minimalPDF(sb.String())); ok {
		t.Fatalf("unmapped glyph indices must not pass as text (%d chars)", len(text))
	}
}

func TestPDFLiteralString_escapesAndNesting(t *testing.T) {
	cases := []struct{ in, want string }{
		{`(plain)`, "plain"},
		{`(a\(nested\) pair)`, "a(nested) pair"},
		{`(outer (inner) back)`, "outer (inner) back"},
		{`(tab\tsep)`, "tab\tsep"},
		{`(\101\102\103)`, "ABC"},
		{`(line\` + "\n" + `continued)`, "linecontinued"},
	}
	for _, c := range cases {
		got, next := pdfLiteralString([]byte(c.in), 0)
		if string(got) != c.want {
			t.Errorf("pdfLiteralString(%q) = %q, want %q", c.in, got, c.want)
		}
		if next > len(c.in) {
			t.Errorf("pdfLiteralString(%q) ran past the input: %d", c.in, next)
		}
	}
}

// Under a CID font a literal string carries two-byte codes; widening
// them to runes during unescaping would destroy the pairing.
func TestPDFLiteralString_returnsRawBytes(t *testing.T) {
	got, _ := pdfLiteralString([]byte(`(\000A\000B)`), 0)
	if !bytes.Equal(got, []byte{0x00, 'A', 0x00, 'B'}) {
		t.Errorf("octal escapes must stay bytes, got % x", got)
	}
}

func TestPDFHexString_decodes(t *testing.T) {
	got, _ := pdfHexString([]byte("<48656C6C6F>"), 0)
	if string(got) != "Hello" {
		t.Errorf("hex string = %q, want Hello", got)
	}
	if got, _ := pdfHexString([]byte("<4A>"), 0); string(got) != "J" {
		t.Errorf("padded hex string = %q, want J", got)
	}
}

// "endstream" contains "stream"; mistaking the closing keyword for an
// opening one desynchronises every later stream in the file.
func TestPDFStreams_skipsTheClosingKeyword(t *testing.T) {
	streams := pdfStreams(minimalPDF(sampleContent))
	if len(streams) != 1 {
		t.Fatalf("want exactly 1 stream, got %d", len(streams))
	}
	if !bytes.Contains(streams[0], []byte("quick brown fox")) {
		t.Errorf("wrong stream body: %q", streams[0])
	}
}

func TestPDFTidy_collapsesBlankRuns(t *testing.T) {
	if got := pdfTidy("first\n\n\n\nsecond   word\n\n"); got != "first\n\nsecond word" {
		t.Errorf("pdfTidy = %q", got)
	}
}

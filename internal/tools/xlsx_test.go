package tools

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// buildXLSX assembles a minimal but real .xlsx in memory: a ZIP of the
// same XML parts Excel writes. Building one beats checking in a binary
// fixture — the test states exactly which shapes it covers, and a public
// repo gains no opaque blob.
func buildXLSX(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const relsNS = `xmlns="http://schemas.openxmlformats.org/package/2006/relationships"`

func reviewWorkbook(t *testing.T) []byte {
	t.Helper()
	return buildXLSX(t, map[string]string{
		"xl/workbook.xml": `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
			<sheets>
				<sheet name="Block 1" sheetId="1" r:id="rId1"/>
				<sheet name="Block 2" sheetId="2" r:id="rId2"/>
			</sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships ` + relsNS + `>
			<Relationship Id="rId1" Target="worksheets/sheet1.xml"/>
			<Relationship Id="rId2" Target="worksheets/sheet2.xml"/>
			</Relationships>`,
		"xl/sharedStrings.xml": `<sst><si><t>Question</t></si><si><t>Scale</t></si>
			<si><r><t>Rich </t></r><r><t>text</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
			<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>5</v></c></row>
			<row r="3"><c r="A3"/><c r="B3"/></row>
			<row r="4"><c r="C4" t="inlineStr"><is><t>only third column</t></is></c></row>
			<row r="5"><c r="A5" t="b"><v>1</v></c><c r="B5" t="b"><v>0</v></c></row>
			</sheetData></worksheet>`,
		"xl/worksheets/_rels/sheet1.xml.rels": `<Relationships ` + relsNS + `>
			<Relationship Id="rId1" Target="../comments1.xml"/></Relationships>`,
		"xl/comments1.xml": `<comments><authors><author>Ada Lovelace</author></authors>
			<commentList>
				<comment ref="B2" authorId="0"><text><r><t>too many </t></r><r><t>options here</t></r></text></comment>
				<comment ref="A1" authorId="0"><text><t>reword this</t></text></comment>
			</commentList></comments>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="inlineStr"><is><t>second sheet</t></is></c></row>
			</sheetData></worksheet>`,
	})
}

func TestXlsxToText_SheetsRowsAndTypes(t *testing.T) {
	got, cut, err := xlsxToText(reviewWorkbook(t))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if cut {
		t.Fatal("a tiny workbook must not report truncation")
	}
	for _, want := range []string{
		"## sheet: Block 1",
		"## sheet: Block 2",
		"Question | Scale", // shared strings, in column order
		"Rich text | 5",    // rich-text runs joined; numeric cell kept
		"only third column",
		"TRUE | FALSE",
		"second sheet",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestXlsxToText_CommentsAreExtracted(t *testing.T) {
	// The point of the feature: a workbook sent for review carries the
	// review in its comments, not in the grid.
	got, _, err := xlsxToText(reviewWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"comments (2)",
		"[A1] Ada Lovelace: reword this",
		"[B2] Ada Lovelace: too many options here", // runs joined
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Comments belong to the sheet that references them, not to sheet 2.
	if i, j := strings.Index(got, "comments (2)"), strings.Index(got, "## sheet: Block 2"); i > j {
		t.Errorf("comments must render under their own sheet:\n%s", got)
	}
}

func TestXlsxToText_BlankRowsDropped(t *testing.T) {
	got, _, err := xlsxToText(reviewWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	// Row 3 is two empty cells; it must not produce a bare separator line.
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == "|" || strings.TrimSpace(line) == "" && strings.Contains(line, "|") {
			t.Errorf("blank row rendered as separator noise:\n%s", got)
		}
	}
}

func TestXlsxToText_RejectsNonWorkbooks(t *testing.T) {
	if _, _, err := xlsxToText([]byte("this is not a zip")); err == nil {
		t.Fatal("garbage must not parse as a workbook")
	}
	// A valid zip that is not a workbook.
	zipOnly := buildXLSX(t, map[string]string{"hello.txt": "hi"})
	if _, _, err := xlsxToText(zipOnly); err == nil {
		t.Fatal("a zip without a workbook part must be rejected")
	}
}

func TestXlsxColumnIndex(t *testing.T) {
	cases := map[string]int{
		"A1": 0, "B7": 1, "Z1": 25, "AA1": 26, "AB10": 27,
		"a3": 0, "": 0, "7": 0,
	}
	for ref, want := range cases {
		if got := xlsxColumnIndex(ref); got != want {
			t.Errorf("xlsxColumnIndex(%q) = %d, want %d", ref, got, want)
		}
	}
}

func TestIsSpreadsheetFile(t *testing.T) {
	yes := []goslack.File{
		docFile("form.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"),
		docFile("form.xlsx", "application/octet-stream", ""),
		docFile("form", "", "xlsx"),
	}
	for _, f := range yes {
		if !isSpreadsheetFile(f) {
			t.Errorf("%s (%q/%q) should be a spreadsheet", f.Name, f.Mimetype, f.Filetype)
		}
		if !isReadableDocument(f) {
			t.Errorf("%s must be readable", f.Name)
		}
		// Binary: the strict sign-in-page guard must still apply.
		if expectsTextBody(f) {
			t.Errorf("%s must not expect a text body", f.Name)
		}
	}
	for _, f := range []goslack.File{
		docFile("notes.txt", "text/plain", "text"),
		docFile("deck.pdf", "application/pdf", "pdf"),
		docFile("pic.png", "image/png", "png"),
	} {
		if isSpreadsheetFile(f) {
			t.Errorf("%s must not be a spreadsheet", f.Name)
		}
	}
}

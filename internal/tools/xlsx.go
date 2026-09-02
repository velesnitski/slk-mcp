package tools

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// An .xlsx workbook is a ZIP of XML parts, so reading one needs nothing
// beyond the standard library. That matters more than convenience here:
// this server is public and parses files handed to it by other people,
// so every dependency it does not have is attack surface it does not
// have. A spreadsheet library would also bring formulas, styles and
// charts — none of which a text renderer wants.
//
// Parts used:
//   xl/workbook.xml               sheet names, in tab order
//   xl/_rels/workbook.xml.rels    sheet name → worksheet part
//   xl/sharedStrings.xml          the string table cells point into
//   xl/worksheets/sheetN.xml      the cells
//   xl/comments*.xml              review comments, resolved per sheet

const (
	// xlsxMaxCells bounds a single workbook. A review questionnaire is
	// hundreds of cells; an export can be hundreds of thousands, and
	// rendering that helps nobody.
	xlsxMaxCells = 20_000
	// xlsxMaxCols guards against a stray cell far to the right turning
	// every row into a wall of separators.
	xlsxMaxCols = 64
)

type xlsxSheet struct {
	Name     string
	Rows     [][]string
	Comments []xlsxComment
}

type xlsxComment struct {
	Cell   string
	Author string
	Text   string
}

// xlsxToText renders a workbook as plain text: one section per sheet,
// rows as pipe-separated cells, then that sheet's comments.
//
// Comments are not decoration. A spreadsheet circulated for review
// carries the review in its comments — the grid is just the thing being
// reviewed — so dropping them would lose the part worth reading. Pure.
func xlsxToText(raw []byte) (string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", false, fmt.Errorf("not a readable .xlsx (zip): %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	shared := xlsxSharedStrings(files)
	sheets, err := xlsxSheets(files, shared)
	if err != nil {
		return "", false, err
	}
	if len(sheets) == 0 {
		return "", false, fmt.Errorf("workbook has no readable sheets")
	}

	var b strings.Builder
	cells := 0
	truncated := false
	for _, sh := range sheets {
		fmt.Fprintf(&b, "\n## sheet: %s\n", sh.Name)
		if len(sh.Rows) == 0 {
			b.WriteString("(empty)\n")
		}
		for _, row := range sh.Rows {
			if cells >= xlsxMaxCells {
				truncated = true
				break
			}
			cells += len(row)
			b.WriteString(strings.Join(row, " | "))
			b.WriteByte('\n')
		}
		if len(sh.Comments) > 0 {
			fmt.Fprintf(&b, "\ncomments (%d):\n", len(sh.Comments))
			for _, c := range sh.Comments {
				author := c.Author
				if author == "" {
					author = "unknown"
				}
				fmt.Fprintf(&b, "  [%s] %s: %s\n", c.Cell, author, c.Text)
			}
		}
		if truncated {
			break
		}
	}
	return strings.TrimSpace(b.String()), truncated, nil
}

// xlsxSharedStrings reads the workbook string table. Cells of type "s"
// hold an index into it rather than the text itself. Rich text splits a
// single string across runs, which are concatenated. Pure.
func xlsxSharedStrings(files map[string]*zip.File) []string {
	f, ok := files["xl/sharedStrings.xml"]
	if !ok {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	var doc struct {
		SI []struct {
			T string   `xml:"t"`
			R []string `xml:"r>t"`
		} `xml:"si"`
	}
	if xml.NewDecoder(rc).Decode(&doc) != nil {
		return nil
	}
	out := make([]string, 0, len(doc.SI))
	for _, si := range doc.SI {
		if len(si.R) > 0 {
			out = append(out, strings.Join(si.R, ""))
			continue
		}
		out = append(out, si.T)
	}
	return out
}

// xlsxSheets resolves sheets in tab order and parses each one.
func xlsxSheets(files map[string]*zip.File, shared []string) ([]xlsxSheet, error) {
	wb, ok := files["xl/workbook.xml"]
	if !ok {
		return nil, fmt.Errorf("not a readable .xlsx (no workbook part)")
	}
	rc, err := wb.Open()
	if err != nil {
		return nil, err
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	decErr := xml.NewDecoder(rc).Decode(&book)
	rc.Close()
	if decErr != nil {
		return nil, fmt.Errorf("workbook part unreadable: %w", decErr)
	}

	rels := xlsxRels(files, "xl/_rels/workbook.xml.rels")
	var out []xlsxSheet
	for _, s := range book.Sheets {
		target := rels[s.RID]
		if target == "" {
			continue
		}
		partName := path.Join("xl", strings.TrimPrefix(target, "/"))
		part, ok := files[partName]
		if !ok {
			continue
		}
		sheet := xlsxSheet{Name: s.Name}
		sheet.Rows = xlsxRows(part, shared)
		sheet.Comments = xlsxComments(files, partName)
		out = append(out, sheet)
	}
	return out, nil
}

// xlsxRels maps relationship ids to targets for one .rels part. Pure
// apart from the zip read.
func xlsxRels(files map[string]*zip.File, name string) map[string]string {
	out := map[string]string{}
	f, ok := files[name]
	if !ok {
		return out
	}
	rc, err := f.Open()
	if err != nil {
		return out
	}
	defer rc.Close()
	var doc struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if xml.NewDecoder(rc).Decode(&doc) != nil {
		return out
	}
	for _, r := range doc.Rel {
		out[r.ID] = r.Target
	}
	return out
}

// xlsxRows parses one worksheet into rows of cell text, preserving
// column position so a value stays under its header.
func xlsxRows(f *zip.File, shared []string) [][]string {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	var sheet struct {
		Rows []struct {
			Cells []struct {
				Ref   string   `xml:"r,attr"`
				Type  string   `xml:"t,attr"`
				V     string   `xml:"v"`
				IS    string   `xml:"is>t"`
				ISRun []string `xml:"is>r>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if xml.NewDecoder(rc).Decode(&sheet) != nil {
		return nil
	}

	var out [][]string
	for _, r := range sheet.Rows {
		row := make([]string, 0, len(r.Cells))
		for _, c := range r.Cells {
			col := xlsxColumnIndex(c.Ref)
			if col >= xlsxMaxCols {
				continue
			}
			for len(row) <= col {
				row = append(row, "")
			}
			row[col] = xlsxCellText(c.Type, c.V, c.IS, c.ISRun, shared)
		}
		// A row of nothing but blanks carries no information and would
		// pad the output with separator noise.
		if strings.TrimSpace(strings.Join(row, "")) == "" {
			continue
		}
		for len(row) > 0 && row[len(row)-1] == "" {
			row = row[:len(row)-1]
		}
		out = append(out, row)
	}
	return out
}

// xlsxCellText resolves one cell to its displayed text. Pure.
func xlsxCellText(typ, v, inline string, inlineRuns, shared []string) string {
	switch typ {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return collapseCell(shared[i])
	case "inlineStr":
		if len(inlineRuns) > 0 {
			return collapseCell(strings.Join(inlineRuns, ""))
		}
		return collapseCell(inline)
	case "b":
		if strings.TrimSpace(v) == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		// Numbers, dates (serial numbers) and formula results land here.
		// Dates are left as serials on purpose: guessing at a format from
		// the style index is how a renderer silently reports a wrong date.
		return collapseCell(v)
	}
}

// collapseCell flattens newlines and runs of space so one cell stays on
// one line. Pure.
func collapseCell(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// xlsxColumnIndex turns a cell reference's column letters into a
// zero-based index: A→0, B→1, AA→26. Returns 0 for a malformed ref, so
// a bad cell lands in the first column rather than dropping out. Pure.
func xlsxColumnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		switch {
		case r >= 'A' && r <= 'Z':
			n = n*26 + int(r-'A') + 1
		case r >= 'a' && r <= 'z':
			n = n*26 + int(r-'a') + 1
		default:
			// digits start the row number: the column part is done
			if n == 0 {
				return 0
			}
			return n - 1
		}
	}
	if n == 0 {
		return 0
	}
	return n - 1
}

// xlsxComments resolves the comments attached to one worksheet, through
// that sheet's own rels part.
func xlsxComments(files map[string]*zip.File, sheetPart string) []xlsxComment {
	relName := path.Join(path.Dir(sheetPart), "_rels", path.Base(sheetPart)+".rels")
	var target string
	for _, t := range xlsxRels(files, relName) {
		if strings.Contains(t, "comments") {
			target = t
			break
		}
	}
	if target == "" {
		return nil
	}
	name := path.Clean(path.Join(path.Dir(sheetPart), target))
	f, ok := files[name]
	if !ok {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	var doc struct {
		Authors  []string `xml:"authors>author"`
		Comments []struct {
			Ref      string   `xml:"ref,attr"`
			AuthorID int      `xml:"authorId,attr"`
			Runs     []string `xml:"text>r>t"`
			Text     string   `xml:"text>t"`
		} `xml:"commentList>comment"`
	}
	if xml.NewDecoder(rc).Decode(&doc) != nil {
		return nil
	}

	var out []xlsxComment
	for _, c := range doc.Comments {
		body := c.Text
		if len(c.Runs) > 0 {
			body = strings.Join(c.Runs, "")
		}
		body = collapseCell(body)
		if body == "" {
			continue
		}
		author := ""
		if c.AuthorID >= 0 && c.AuthorID < len(doc.Authors) {
			author = doc.Authors[c.AuthorID]
		}
		out = append(out, xlsxComment{Cell: c.Ref, Author: author, Text: body})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Cell < out[j].Cell })
	return out
}

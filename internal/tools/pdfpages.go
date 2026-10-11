package tools

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Per-page text coverage for PDFs. See ADR 117.
//
// The text extractor reads every content stream in the file and has no
// idea which page a stream belongs to. That is fine for prose, but a
// slide deck mixes text slides with slides that are a single picture of
// a table, and those pictures vanish from the extracted text without a
// trace: the reader gets a document that looks complete and is not.
// This walks the page tree, reads each page's own content, and names
// the pages that carry almost no text but draw an image.

// pdfMaxPages bounds the walk on a malformed or hostile file.
const pdfMaxPages = 2000

// pdfPageTextMinLetters is the least alphabetic text a page needs to
// count as having a text layer. A slide that is only a picture of a
// table still carries its title ("Results"), so the bar sits
// above a short heading.
const pdfPageTextMinLetters = 20

var (
	pdfObjHeaderRe = regexp.MustCompile(`(\d+)\s+(\d+)\s+obj\b`)
	pdfRefRe       = regexp.MustCompile(`(\d+)\s+\d+\s+R`)
	pdfRootRe      = regexp.MustCompile(`/Root\s+(\d+)\s+\d+\s+R`)
	pdfPagesRefRe  = regexp.MustCompile(`/Pages\s+(\d+)\s+\d+\s+R`)
	pdfKidsRe      = regexp.MustCompile(`/Kids\s*\[([^\]]*)\]`)
	pdfTypePageRe  = regexp.MustCompile(`/Type\s*/Page(?:[^s]|$)`)
	pdfTypePagesRe = regexp.MustCompile(`/Type\s*/Pages\b`)
	pdfContentsRe  = regexp.MustCompile(`/Contents\s*(?:(\d+)\s+\d+\s+R|\[([^\]]*)\])`)
	pdfResRefRe    = regexp.MustCompile(`/Resources\s+(\d+)\s+\d+\s+R`)
	pdfXObjInline  = regexp.MustCompile(`/XObject\s*<<([^>]*)>>`)
	pdfXObjRefRe   = regexp.MustCompile(`/XObject\s+(\d+)\s+\d+\s+R`)
	pdfObjStmNRe   = regexp.MustCompile(`/N\s+(\d+)`)
	pdfObjStmFirst = regexp.MustCompile(`/First\s+(\d+)`)
	pdfDoOpRe      = regexp.MustCompile(`(?:^|\s)/[^\s/]+\s+Do\b`)
)

// pdfObjects indexes a PDF's objects by number: each object's dictionary
// text and, for stream objects, its raw stream body. Objects packed into
// object streams (/Type /ObjStm, PDF 1.5+) are unpacked too — that is
// where modern exporters keep the page dictionaries.
type pdfObjects struct {
	dict   map[int][]byte
	stream map[int][]byte
}

func indexPDFObjects(raw []byte) *pdfObjects {
	o := &pdfObjects{dict: map[int][]byte{}, stream: map[int][]byte{}}
	locs := pdfObjHeaderRe.FindAllSubmatchIndex(raw, -1)
	for i, loc := range locs {
		n, err := strconv.Atoi(string(raw[loc[2]:loc[3]]))
		if err != nil {
			continue
		}
		end := len(raw)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := raw[loc[1]:end]
		if e := bytes.Index(body, []byte("endobj")); e >= 0 {
			body = body[:e]
		}
		if s := bytes.Index(body, []byte("stream")); s >= 0 {
			o.dict[n] = body[:s]
			sb := bytes.TrimLeft(body[s+len("stream"):], "\r\n")
			if e := bytes.Index(sb, []byte("endstream")); e >= 0 {
				sb = sb[:e]
			}
			o.stream[n] = sb
		} else {
			o.dict[n] = body
		}
	}
	for n, d := range o.dict {
		if bytes.Contains(d, []byte("/ObjStm")) {
			o.unpackObjStm(o.stream[n], d)
		}
	}
	return o
}

// unpackObjStm adds the objects of one object stream. Its header is N
// pairs of "objnum offset", and the objects start at byte /First.
func (o *pdfObjects) unpackObjStm(body, dict []byte) {
	nm, fm := pdfObjStmNRe.FindSubmatch(dict), pdfObjStmFirst.FindSubmatch(dict)
	if nm == nil || fm == nil {
		return
	}
	count, _ := strconv.Atoi(string(nm[1]))
	first, _ := strconv.Atoi(string(fm[1]))
	data, ok := pdfDecodeStream(body)
	if !ok || first <= 0 || first > len(data) || count <= 0 || count > pdfMaxStreams {
		return
	}
	fields := strings.Fields(string(data[:first]))
	type entry struct{ num, off int }
	var entries []entry
	for i := 0; i+1 < len(fields) && len(entries) < count; i += 2 {
		num, e1 := strconv.Atoi(fields[i])
		off, e2 := strconv.Atoi(fields[i+1])
		if e1 != nil || e2 != nil {
			return
		}
		entries = append(entries, entry{num, off})
	}
	for i, e := range entries {
		start := first + e.off
		end := len(data)
		if i+1 < len(entries) {
			end = first + entries[i+1].off
		}
		if start < 0 || start > end || end > len(data) {
			continue
		}
		if _, exists := o.dict[e.num]; !exists {
			o.dict[e.num] = data[start:end]
		}
	}
}

// pages returns the page dictionaries' object numbers in reading order,
// by walking /Root → /Pages → /Kids. Nil when the tree cannot be read.
func (o *pdfObjects) pages(raw []byte) []int {
	roots := pdfRootRe.FindAllSubmatch(raw, -1)
	if len(roots) == 0 {
		return nil
	}
	root, _ := strconv.Atoi(string(roots[len(roots)-1][1]))
	pm := pdfPagesRefRe.FindSubmatch(o.dict[root])
	if pm == nil {
		return nil
	}
	top, _ := strconv.Atoi(string(pm[1]))
	var out []int
	seen := map[int]bool{}
	var walk func(n int)
	walk = func(n int) {
		if seen[n] || len(out) >= pdfMaxPages {
			return
		}
		seen[n] = true
		d := o.dict[n]
		switch {
		case pdfTypePagesRe.Match(d):
			if km := pdfKidsRe.FindSubmatch(d); km != nil {
				for _, r := range pdfRefRe.FindAllSubmatch(km[1], -1) {
					k, _ := strconv.Atoi(string(r[1]))
					walk(k)
				}
			}
		case pdfTypePageRe.Match(d):
			out = append(out, n)
		}
	}
	walk(top)
	return out
}

// pageContent concatenates a page's decoded content streams, plus those
// of the form XObjects it draws, one level deep: some exporters put a
// slide's text inside a form, and counting only the page stream would
// call that slide empty.
func (o *pdfObjects) pageContent(page int) (content []byte, drawsXObject bool) {
	d := o.dict[page]
	var b bytes.Buffer
	for _, n := range o.contentRefs(d) {
		if s, ok := pdfDecodeStream(o.stream[n]); ok {
			b.Write(s)
			b.WriteByte('\n')
		}
	}
	main := b.Bytes()
	drawsXObject = pdfDoOpRe.Match(main)
	if drawsXObject {
		for _, n := range o.xobjectRefs(d) {
			xd := o.dict[n]
			if !bytes.Contains(xd, []byte("/Form")) {
				continue
			}
			if s, ok := pdfDecodeStream(o.stream[n]); ok {
				b.Write(s)
				b.WriteByte('\n')
			}
		}
	}
	return b.Bytes(), drawsXObject
}

func (o *pdfObjects) contentRefs(page []byte) []int {
	m := pdfContentsRe.FindSubmatch(page)
	if m == nil {
		return nil
	}
	if len(m[1]) > 0 {
		n, _ := strconv.Atoi(string(m[1]))
		// /Contents may point at an array object rather than a stream.
		if _, isStream := o.stream[n]; !isStream {
			var out []int
			for _, r := range pdfRefRe.FindAllSubmatch(o.dict[n], -1) {
				k, _ := strconv.Atoi(string(r[1]))
				out = append(out, k)
			}
			return out
		}
		return []int{n}
	}
	var out []int
	for _, r := range pdfRefRe.FindAllSubmatch(m[2], -1) {
		k, _ := strconv.Atoi(string(r[1]))
		out = append(out, k)
	}
	return out
}

func (o *pdfObjects) xobjectRefs(page []byte) []int {
	res := page
	if rm := pdfResRefRe.FindSubmatch(page); rm != nil {
		n, _ := strconv.Atoi(string(rm[1]))
		res = o.dict[n]
	}
	var table []byte
	if m := pdfXObjInline.FindSubmatch(res); m != nil {
		table = m[1]
	} else if m := pdfXObjRefRe.FindSubmatch(res); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		table = o.dict[n]
	}
	var out []int
	for _, r := range pdfRefRe.FindAllSubmatch(table, -1) {
		k, _ := strconv.Atoi(string(r[1]))
		out = append(out, k)
	}
	return out
}

// pdfImageOnlyPages returns the 1-based numbers of pages that carry
// almost no text but draw an image or form — the pages a reader of the
// extracted text never sees — plus the page count. Nil pages when the
// page tree cannot be read; the caller then says nothing rather than
// guess.
func pdfImageOnlyPages(raw []byte, cm *pdfCMap) (pages []int, total int) {
	o := indexPDFObjects(raw)
	order := o.pages(raw)
	if len(order) == 0 {
		return nil, 0
	}
	for i, n := range order {
		content, draws := o.pageContent(n)
		if !draws {
			continue
		}
		if pdfLetters(pdfTidy(pdfContentText(content, cm))) < pdfPageTextMinLetters {
			pages = append(pages, i+1)
		}
	}
	return pages, len(order)
}

func pdfLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// pdfPageRanges renders sorted page numbers compactly: 35–40, 42, 44–45.
func pdfPageRanges(pages []int) string {
	sort.Ints(pages)
	var parts []string
	for i := 0; i < len(pages); {
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d–%d", pages[i], pages[j]))
		} else {
			parts = append(parts, strconv.Itoa(pages[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

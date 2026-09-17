package tools

import (
	"bytes"
	"compress/zlib"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// pdfMaxStreams bounds how many streams one document may contribute. A
// malformed or hostile file should cost bounded work.
const pdfMaxStreams = 4000

// pdfMinLetters is the least alphabetic text an extraction must yield
// before we call it a result. Below it we assume there is no readable
// text layer and hand back the file rather than a page of noise.
const pdfMinLetters = 40

// pdfCMap is a /ToUnicode table: the map a PDF ships so that the codes
// in its content streams can be turned back into characters.
//
// It is needed because a subset-embedded font (Identity-H, the common
// case for anything generated from a word processor) does not put
// characters in the content stream at all — it puts glyph indices, whose
// numbering is private to that one font. The offset between a glyph
// index and its character is font-specific, so reading the text without
// this table means guessing, and a guess that happens to work on an
// English document silently mangles a Cyrillic one.
type pdfCMap struct {
	m         map[uint32]string
	codeBytes int
}

// extractPDFText pulls the visible text out of a PDF without taking a
// dependency: it inflates every stream, builds the /ToUnicode table from
// whichever of them are CMaps, and reads the string operands of the
// text-showing operators through it.
//
// Two passes are tried, CMap first and raw bytes second, because a
// document with simple fonts ships no CMap and encodes characters
// directly. The bool reports whether the result is worth showing; false
// means there is no text layer at all — a scan — and the caller should
// fall back to handing over the file, which for those is the only way to
// read the content.
//
// Exact figures should still be checked against the document: this
// reproduces characters, not layout, and a number lifted out of a table
// is only as trustworthy as the column it came from.
func extractPDFText(raw []byte) (string, bool) {
	var decoded [][]byte
	for _, s := range pdfStreams(raw) {
		if d, ok := pdfDecodeStream(s); ok {
			decoded = append(decoded, d)
		}
	}
	if len(decoded) == 0 {
		return "", false
	}

	modes := make([]*pdfCMap, 0, 2)
	if cm := pdfBuildToUnicode(decoded); cm != nil {
		modes = append(modes, cm)
	}
	modes = append(modes, nil)

	for _, cm := range modes {
		var b strings.Builder
		for _, d := range decoded {
			if pdfIsCMapStream(d) {
				continue
			}
			b.WriteString(pdfContentText(d, cm))
		}
		if text := pdfTidy(b.String()); pdfTextLooksUsable(text) {
			return text, true
		}
	}
	return "", false
}

// pdfStreams returns the bytes between each `stream` / `endstream` pair.
// Matches preceded by "end" are skipped so the closing keyword cannot be
// mistaken for an opening one, which would desynchronise every stream
// after it.
func pdfStreams(raw []byte) [][]byte {
	var out [][]byte
	rest := raw
	for len(out) < pdfMaxStreams {
		i := bytes.Index(rest, []byte("stream"))
		if i < 0 {
			break
		}
		if i >= 3 && bytes.Equal(rest[i-3:i], []byte("end")) {
			rest = rest[i+len("stream"):]
			continue
		}
		body := bytes.TrimLeft(rest[i+len("stream"):], "\r\n")
		j := bytes.Index(body, []byte("endstream"))
		if j < 0 {
			break
		}
		out = append(out, body[:j])
		rest = body[j+len("endstream"):]
	}
	return out
}

// pdfDecodeStream inflates a FlateDecode stream. An uncompressed stream
// is accepted only when it looks like one we can use, so that image and
// font payloads are not fed to the text scanner as if they were prose.
func pdfDecodeStream(stream []byte) ([]byte, bool) {
	stream = bytes.TrimSpace(stream)
	if len(stream) == 0 {
		return nil, false
	}
	if zr, err := zlib.NewReader(bytes.NewReader(stream)); err == nil {
		defer zr.Close()
		// A truncated stream still yields what was read before the
		// error, and partial text beats no text.
		out, _ := io.ReadAll(zr)
		if len(out) > 0 {
			return out, true
		}
	}
	for _, marker := range [][]byte{[]byte("Tj"), []byte("TJ"), []byte("beginbfchar"), []byte("beginbfrange")} {
		if bytes.Contains(stream, marker) {
			return stream, true
		}
	}
	return nil, false
}

func pdfIsCMapStream(d []byte) bool {
	return bytes.Contains(d, []byte("beginbfchar")) || bytes.Contains(d, []byte("beginbfrange"))
}

var (
	pdfBFCharBlock   = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	pdfBFRangeBlock  = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	pdfHexPair       = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	pdfHexTriple     = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	pdfHexArrayRange = regexp.MustCompile(`(?s)<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*\[(.*?)\]`)
	pdfHexToken      = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
)

// pdfBuildToUnicode merges every /ToUnicode CMap in the document into
// one table. Merging rather than resolving font-by-font is deliberate:
// binding each text run to its font means resolving object references
// and tracking Tf operators, and documents that carry several subset
// fonts overwhelmingly come from one generator that numbers them
// consistently. Returns nil when the document ships no CMap.
func pdfBuildToUnicode(streams [][]byte) *pdfCMap {
	cm := &pdfCMap{m: make(map[uint32]string)}
	for _, s := range streams {
		if !pdfIsCMapStream(s) {
			continue
		}
		text := string(s)
		for _, blk := range pdfBFCharBlock.FindAllStringSubmatch(text, -1) {
			for _, p := range pdfHexPair.FindAllStringSubmatch(blk[1], -1) {
				code, width := pdfHexValue(p[1])
				cm.noteWidth(width)
				cm.m[code] = pdfUTF16BEString(p[2])
			}
		}
		for _, blk := range pdfBFRangeBlock.FindAllStringSubmatch(text, -1) {
			body := blk[1]
			// Array form: <lo> <hi> [<d0> <d1> ...] — one destination
			// per code, so it must be consumed before the triple form
			// below can mistake its first two tokens for a range.
			for _, a := range pdfHexArrayRange.FindAllStringSubmatch(body, -1) {
				lo, width := pdfHexValue(a[1])
				cm.noteWidth(width)
				for i, d := range pdfHexToken.FindAllStringSubmatch(a[3], -1) {
					cm.m[lo+uint32(i)] = pdfUTF16BEString(d[1])
				}
			}
			body = pdfHexArrayRange.ReplaceAllString(body, "")
			// Triple form: <lo> <hi> <dstStart>, incrementing the last
			// unit of the destination across the range.
			for _, tr := range pdfHexTriple.FindAllStringSubmatch(body, -1) {
				lo, width := pdfHexValue(tr[1])
				hi, _ := pdfHexValue(tr[2])
				cm.noteWidth(width)
				base := pdfUTF16BERunes(tr[3])
				if len(base) == 0 || hi < lo || hi-lo > 0xFFFF {
					continue
				}
				for code := lo; code <= hi; code++ {
					r := append([]rune(nil), base...)
					r[len(r)-1] += rune(code - lo)
					cm.m[code] = string(r)
				}
			}
		}
	}
	if len(cm.m) == 0 {
		return nil
	}
	if cm.codeBytes == 0 {
		cm.codeBytes = 2
	}
	return cm
}

func (c *pdfCMap) noteWidth(width int) {
	if width > c.codeBytes {
		c.codeBytes = width
	}
}

// pdfLineEpsilon is how far the text baseline must move, in unscaled
// text units, before the output starts a new line. Well below a line's
// leading, well above the rounding in a positioning operand.
const pdfLineEpsilon = 0.5

// pdfContentText walks a decoded content stream and concatenates the
// strings handed to the text operators, breaking lines where the text
// baseline actually moves.
//
// Line breaks have to be driven by the vertical position rather than by
// the presence of a positioning operator. A generator is free to place
// every single glyph with its own `Td` — `0 -12 Td <0028> Tj` then
// `8.88 0 Td <0050> Tj` — and treating each one as a line break puts one
// character on each line. It is equally free to wrap each word in its
// own BT/ET pair, so breaking on `ET` splits words that share a line.
// Tracking the baseline handles both: within a line the vertical
// component is zero, between lines it is not.
func pdfContentText(content []byte, cm *pdfCMap) string {
	var b strings.Builder
	var operands []float64
	var originY, offsetY, lastY float64
	var positioned bool

	show := func(raw []byte) {
		y := originY + offsetY
		if positioned && math.Abs(y-lastY) > pdfLineEpsilon {
			b.WriteByte('\n')
		}
		lastY = y
		positioned = true
		b.WriteString(pdfDecodeText(raw, cm))
	}

	for i := 0; i < len(content); {
		c := content[i]
		switch {
		case c == '(':
			s, next := pdfLiteralString(content, i)
			show(s)
			i = next
		case c == '<' && i+1 < len(content) && content[i+1] != '<':
			s, next := pdfHexString(content, i)
			show(s)
			i = next
		case c == '/':
			// A name (/F4, /P) — skip it so it is not read as an operator.
			i++
			for i < len(content) && !isPDFDelimiter(content[i]) {
				i++
			}
		case isPDFNumberStart(c):
			j := i
			for j < len(content) && isPDFNumberByte(content[j]) {
				j++
			}
			if v, err := strconv.ParseFloat(string(content[i:j]), 64); err == nil {
				operands = append(operands, v)
				if len(operands) > 8 {
					operands = operands[len(operands)-8:]
				}
			}
			i = j
		case isPDFOperatorByte(c):
			j := i
			for j < len(content) && isPDFOperatorByte(content[j]) {
				j++
			}
			switch op := string(content[i:j]); op {
			case "Tm":
				// a b c d e f: f is the vertical translation, and it
				// restarts the line-relative offset.
				if len(operands) >= 6 {
					originY = operands[len(operands)-1]
				}
				offsetY = 0
			case "Td", "TD":
				// tx ty, relative to the current line origin, and
				// cumulative until the next Tm.
				if len(operands) >= 2 {
					offsetY += operands[len(operands)-1]
				}
			case "T*", "'", "\"":
				// An explicit next-line operator; the leading is not
				// tracked, so move by enough to register as a break.
				offsetY -= 2 * pdfLineEpsilon
			}
			operands = operands[:0]
			i = j
		default:
			i++
		}
	}
	return b.String()
}

func isPDFNumberStart(c byte) bool {
	return (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.'
}

func isPDFNumberByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E'
}

func isPDFOperatorByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '*' || c == '\'' || c == '"'
}

func isPDFDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f', 0, '/', '[', ']', '<', '>', '(', ')', '{', '}', '%':
		return true
	}
	return false
}

// pdfDecodeText turns the raw bytes of a PDF string into characters.
// With a CMap the bytes are fixed-width codes to look up; without one
// they are character codes in a byte encoding, which Latin-1 reproduces
// for the ASCII range every such document actually uses.
func pdfDecodeText(s []byte, cm *pdfCMap) string {
	var b strings.Builder
	if cm != nil && cm.codeBytes == 2 {
		for i := 0; i+1 < len(s); i += 2 {
			if t, ok := cm.m[uint32(s[i])<<8|uint32(s[i+1])]; ok {
				b.WriteString(t)
			}
		}
		return b.String()
	}
	for _, c := range s {
		b.WriteRune(rune(c))
	}
	return b.String()
}

// pdfLiteralString decodes a `(...)` string starting at the opening
// paren, returning the raw bytes and the index just past the close.
// Bytes, not a string: under a CID font these are two-byte codes, and
// widening them to runes here would destroy the pairing.
func pdfLiteralString(b []byte, start int) ([]byte, int) {
	var out []byte
	depth := 0
	for i := start; i < len(b); {
		c := b[i]
		if c == '\\' && i+1 < len(b) {
			i++
			switch e := b[i]; {
			case e == 'n':
				out = append(out, '\n')
			case e == 'r':
				out = append(out, '\r')
			case e == 't':
				out = append(out, '\t')
			case e == 'b':
				out = append(out, '\b')
			case e == 'f':
				out = append(out, '\f')
			case e == '\n':
				// an escaped newline is a line continuation
			case e == '\r':
				if i+1 < len(b) && b[i+1] == '\n' {
					i++
				}
			case e >= '0' && e <= '7':
				v, n := 0, 0
				for n < 3 && i < len(b) && b[i] >= '0' && b[i] <= '7' {
					v = v*8 + int(b[i]-'0')
					i++
					n++
				}
				i--
				out = append(out, byte(v))
			default:
				out = append(out, e)
			}
			i++
			continue
		}
		switch c {
		case '(':
			depth++
			if depth > 1 {
				out = append(out, c)
			}
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
		i++
	}
	return out, len(b)
}

// pdfHexString decodes a `<...>` string starting at the opening angle
// bracket. An odd trailing digit is padded with zero, per the spec.
func pdfHexString(b []byte, start int) ([]byte, int) {
	var hex []byte
	i := start + 1
	for ; i < len(b) && b[i] != '>'; i++ {
		if isHexDigit(b[i]) {
			hex = append(hex, b[i])
		}
	}
	if i < len(b) {
		i++
	}
	return pdfHexBytes(string(hex)), i
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func pdfHexBytes(h string) []byte {
	if len(h)%2 == 1 {
		h += "0"
	}
	out := make([]byte, 0, len(h)/2)
	for i := 0; i+1 < len(h); i += 2 {
		v, err := strconv.ParseUint(h[i:i+2], 16, 8)
		if err != nil {
			return out
		}
		out = append(out, byte(v))
	}
	return out
}

func pdfHexValue(h string) (uint32, int) {
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return 0, len(h) / 2
	}
	return uint32(v), len(h) / 2
}

// pdfUTF16BERunes decodes a CMap destination, which the spec defines as
// big-endian UTF-16 and which may hold more than one character — a
// ligature maps one code to several.
func pdfUTF16BERunes(h string) []rune {
	b := pdfHexBytes(h)
	if len(b)%2 == 1 {
		b = append(b, 0)
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return utf16.Decode(u)
}

func pdfUTF16BEString(h string) string { return string(pdfUTF16BERunes(h)) }

// pdfTidy collapses the whitespace extraction produces: runs of spaces
// become one, trailing space goes, and a run of blank lines becomes a
// single blank line.
func pdfTidy(s string) string {
	var out []string
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// pdfTextLooksUsable rejects the output of a PDF with no readable text
// layer. Reading glyph indices as characters yields plenty of bytes and
// almost no letters; handing back the file is the honest answer there.
func pdfTextLooksUsable(text string) bool {
	var letters, printable, total int
	for _, r := range text {
		total++
		if unicode.IsLetter(r) {
			letters++
		}
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			printable++
		}
	}
	if total == 0 || letters < pdfMinLetters {
		return false
	}
	return float64(printable)/float64(total) >= 0.9
}

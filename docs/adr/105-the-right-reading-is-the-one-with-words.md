# ADR 105: the right reading of a PDF is the one with words

Date: 2026-09-17
Status: accepted

## Context

ADR 104 added PDF text extraction and tried the two readings in order:
map the bytes through the document's /ToUnicode table, and if that did
not clear a quality bar, read them as character codes. Testing it
against real documents showed the order is wrong, and that the quality
bar measured the wrong thing.

A word-processor PDF embeds subset fonts and is unreadable without its
CMap. A PDF whose body text uses simple fonts encodes characters
directly and still ships a CMap — covering a handful of symbols, a
bullet and a trademark sign. Taking that CMap yields a page of bullets
while several hundred thousand characters of document sit in the reading
that was never tried, because the first reading cleared the bar.

The bar cleared because it asked whether the output was mostly printable
and had at least a few dozen letters. A wrong reading passes both: it
produces characters, just not words. The distinguishing property is
whether letters group into runs.

A third defect surfaced at the same time. Embedded font programs and
images inflate exactly like page content, and a binary payload contains
`(` and `<` bytes by chance, so scanning one as a content stream appends
a tail of noise to the real text. The only reliable way to tell them
apart is the stream's dictionary — and reading that dictionary as a
fixed window of preceding bytes does not work either: the window reaches
back into whatever object precedes this one, so a content stream that
happens to follow a font descriptor is discarded as a font, taking the
whole page with it.

## Decision

Both readings run, and the one with more word-shaped runs wins. Which
reading is correct is a property of the document, not a fallback order,
so there is no order to get wrong.

`pdfWordScore` counts runs of three or more consecutive letters, and is
also the quality gate: a reading must produce at least a few of them to
count as text at all. Control bytes are stripped before scoring rather
than being counted against the result, because they are never part of
the document and judging text by how much binary came with it measures
the wrong thing.

Streams are classified by their own dictionary, delimited by matching
`<<`/`>>` and parsed backwards from the `stream` keyword — not by a byte
window. `/Length1`, `/FontFile`, an image subtype or an image filter
means the stream is a resource, not content. A dictionary that cannot be
found is treated as content, so a malformed file loses no text.

## Consequences

- Both document classes read correctly: the subset-font case through its
  CMap, the simple-font case through direct codes.
- A wrong reading is no longer reported as success. A PDF that yields
  characters but no words falls back to the saved path, as a scan does.
- Extraction still reproduces characters, not layout, and some documents
  carry stray symbols between text runs where the content stream holds
  bytes that are neither operators nor prose. The rendered header keeps
  telling the reader to check exact figures against the document.
- Scoring both readings means extracting twice. On a large document that
  is measurable and still far below the cost of fetching it.

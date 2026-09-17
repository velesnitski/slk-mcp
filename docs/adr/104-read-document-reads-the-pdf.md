# ADR 104: read_document reads the PDF

Date: 2026-09-17
Status: accepted

## Context

`read_document` flattened every document type it accepted except one.
A PDF was downloaded, left on disk, and reported by path, with a note
telling the caller to open it with a PDF-capable reader. The reasoning
recorded at the time was that parsing PDF in Go means a dependency and a
lossy extraction, when the caller already has a reader that renders PDFs
properly.

That reasoning held for the text, and missed two consequences.

First, a PDF was the only type that outlived its call. Every other
document is read, rendered and deleted; nothing needs to survive. A PDF
stayed in the temp directory indefinitely, so the one format that
carries contracts, invoices and lab reports was also the one format that
accumulated on disk. The containing directory is user-scoped, so this
was never an exposure to other users — but the file was written with
`os.Create`, whose 0666-and-umask is looser than the 0600 this codebase
uses elsewhere for exactly this kind of content, and the pile grows for
as long as the server is used.

Second, and worse, the text path applies `export.Redact` before
rendering, so key material in a `.conf` or a `.ovpn` never reaches the
transcript. A PDF skipped that step by construction. A PDF carrying a
credential was handed back as a path to a file that still contained it.

Third, the tool's name is a promise. Returning "binary document, not
flattened to text" is a tool declining to do the thing it is named for,
and every caller then needs a second, out-of-band step.

## Decision

`extractPDFText` flattens PDFs with no new dependency: inflate every
stream, build the document's own `/ToUnicode` table from whichever
streams are CMaps, and read the string operands of the text operators
through it.

Reading the bytes directly is not an option worth shipping. A
subset-embedded font — Identity-H, which is what anything generated from
a word processor uses — puts glyph indices in the content stream, not
characters, and the offset between an index and its character is private
to that font. Guessing it produces plausible English and silently
mangles everything else. The `/ToUnicode` table is the mapping the
document itself ships for this purpose, so it is the thing to use.

Line breaks follow the text baseline, not the presence of a positioning
operator. A generator may place every glyph with its own `Td`, and may
wrap every word in its own `BT`/`ET` pair; breaking on either operator
yields one character per line, or words split across lines. The vertical
component is zero within a line and non-zero between lines, which is the
signal that actually means "new line".

Extraction is best-effort and says so. A usability gate rejects output
with too few letters — a scan, or a font whose CMap is absent — and the
caller falls back to the saved path, because for those the file really is
the only way to read the content. Extracted text passes through
`export.Redact` and the same truncation as every other document, and the
file is deleted once it has been read. Downloads are created 0600.

## Consequences

- A PDF reads inline like every other document, including Cyrillic and
  any other non-Latin text, which a byte-level reading would have
  corrupted.
- PDFs stop accumulating in the temp directory, and stop bypassing
  secret redaction.
- Extraction reproduces characters, not layout. The rendered header says
  so: a figure read out of a table should be checked against the
  document, and a PDF with no text layer still comes back as a path.
- No new module dependency; the cost is roughly 300 lines of parser this
  repo now owns.

# ADR 117: name the PDF pages the text leaves out

Date: 2026-10-11
Status: accepted

## Context

A long slide deck read through `read_document` came back as clean,
plausible text, and the reader built an analysis on it. Two gaps were
invisible:

- Several slides were pictures of tables, a whole section among them.
  The extractor reads content streams with no notion of pages,
  so a picture slide contributes nothing and leaves no trace — the text
  looks complete and is not.
- The 40,000-character cap cut the end of the document. The note said
  "TRUNCATED to 40000 chars" without saying how long the document was,
  so a trimmed tail looked like a small one.

## Decision

- Walk the page tree (`/Root → /Pages → /Kids`), including dictionaries
  packed into compressed object streams, read each page's own content
  streams plus the form XObjects it draws (one level), and flag pages
  with fewer than 20 letters that draw an XObject.
- When pages are flagged, the output names them as page ranges and keeps
  the file on disk with its path, so they can be read visually. With
  nothing flagged the file is deleted as before.
- The header now carries the page count, and the truncation note says
  `TRUNCATED to N of M chars (pass max_chars=M for all of it)`, for every
  document type.
- A PDF whose page tree cannot be read gets no report rather than a
  guess.

## Consequences

- Slide decks with image slides announce them instead of silently
  dropping them.
- Section-divider slides (a title over a full-page picture) are flagged
  too: neither letter count nor image size separates them from a
  pictured table. The note says some may be title slides; a false flag
  costs a glance, a missed one costs a wrong analysis.
- When pages are flagged the PDF outlives the call, like a scanned PDF
  already did.

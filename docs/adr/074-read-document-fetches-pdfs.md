# ADR 074: read_document fetches PDFs instead of refusing them

Date: 2026-08-12
Status: accepted

## Context

Two decks arrived in a channel as PDF and `read_document` answered "no
recent message with a matching attachment" — `isDocumentFile` accepts
`text/*` and the textual `application/*` types, and a PDF is neither.

That answer is wrong in the way that matters: a PDF *is* a document, and
in practice it is the format decisions circulate in. Proposals, forecast
decks and reports are PDFs. Refusing them leaves the same hole ADR 071
was written to close, one file type over.

## Decision

Accept PDFs, but do not parse them.

`isReadableDocument` = text (unchanged) plus `isPDFFile`. A PDF is
downloaded like any other attachment and then **reported by local path**
rather than flattened; text documents keep rendering inline and are
deleted immediately after.

Parsing PDF in Go would mean a dependency and a lossy extraction that
drops exactly what a deck carries — layout, tables, figures. The caller
already has a reader that renders PDFs properly, so handing over a path
is both smaller and better. This is the `download_audio` contract reused:
the token never leaves the server, only a local path crosses back.

Two consequences follow from keeping the file:

- Downloads go to the shared temp dir instead of a private `MkdirTemp`
  that was removed wholesale, since a PDF must outlive the call.
- `expectsTextBody` deliberately still returns false for PDFs, so the
  strict "an HTML body means a missing files:read scope" guard (ADR 071)
  keeps applying to them. Only genuine text files get the relaxed
  sign-in-page check.

## Consequences

- Decks and reports are readable in-session; the tool no longer denies
  the existence of an attachment that is plainly there.
- A PDF is left in the temp dir after the call. Bounded by the OS temp
  cleanup, and it is the same footprint `download_audio` already has.
- Office formats (docx, xlsx, pptx) are still refused. They would need
  the same treatment plus a reader that handles them; out of scope until
  one shows up in practice.
- 619 → 621 tests. Minor release (1.31.0).

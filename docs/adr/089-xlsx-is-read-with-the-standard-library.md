# ADR 089: .xlsx is read with the standard library, comments included

Date: 2026-09-02
Status: accepted

## Context

Spreadsheets circulate constantly — review questionnaires, ops summaries,
cost tables — and `read_document` could not open one. A colleague's
"please comment on sheets 1/2/3" was unanswerable without leaving the
session, which is the same gap ADR 081 closed for `.log` files.

## Decision

**Parse it with `archive/zip` and `encoding/xml`, not a library.** An
.xlsx is a ZIP of XML parts, and text extraction needs four of them. A
spreadsheet library would bring formulas, styles, charts and a
dependency tree into a server that has two direct dependencies and is
public — and this code parses files handed to it by other people, so
every dependency it does not have is attack surface it does not have.
The trade is real: no formula evaluation, no style-driven date
formatting.

**Dates stay as serial numbers.** Inferring a format from the style
index is how a renderer silently reports the wrong date. A visible
serial is honest; a plausible wrong date is not.

**Comments are extracted, per sheet.** This is the point rather than a
nicety: a workbook sent for review carries the review in its comments —
the grid is the thing being reviewed. Comments are resolved through each
worksheet's own rels part, so they land under the sheet they belong to.

**It stays binary.** `.xlsx` is deliberately not in the text-extension
list and `expectsTextBody` excludes it, so the sign-in-page download
guard stays strict for it exactly as for a PDF. Unlike a PDF it is
rendered inline, because it flattens to useful text.

Caps: 20 000 cells per workbook and 64 columns per row, both reported
when hit. An export can be hundreds of thousands of cells, and a single
stray far-right cell would otherwise turn every row into separators.

## Consequences

- Review forms, ops tables and cost sheets are readable in place, with
  the review comments attached.
- No new dependency; `go.mod` still lists two direct requirements.
- Tests build a real workbook in memory rather than checking in a binary
  fixture, so the covered shapes are stated in the test and the repo
  gains no opaque blob.
- Not covered: formulas are read as their cached result, styles and
  number formats are ignored, and charts and images are skipped.

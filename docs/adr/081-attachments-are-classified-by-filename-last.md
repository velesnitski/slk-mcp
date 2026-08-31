# ADR 081: attachments fall back to the filename, and a listing never omits

Date: 2026-08-31
Status: accepted

## Context

`read_document` classified attachments by mimetype, then by Slack's own
`filetype`. Both tiers describe what Slack *believes* it received, and
Slack believes very little: anything outside its own extension table
arrives as `application/octet-stream` with `filetype: binary`.

That is the normal shape of operational evidence. A pair of client logs
attached to a test report were skipped by both tiers — and the skip was
reported only in the read path, as a trailing note. `list_only` filtered
the same predicate *before* rendering, so the listing showed the
attachments it could read and nothing else. Asked what a conversation
held, the tool answered with a confident, complete-looking, incomplete
list. Nothing in the output distinguished "these are the four files" from
"these are the four of six files I happen to handle".

## Decision

**A third classification tier: the filename.** Consulted only when the
mimetype is generic (empty, `application/octet-stream` and friends).
A generic mimetype is an absence of information, not a claim that the
bytes are binary, so the extension is free to decide. An explicit
`image/*`, `audio/*`, `video/*` or `application/zip` remains a statement
about the bytes and still wins — a `.log` suffix on a PNG does not make
it text.

**A listing lists everything.** `list_only` now accepts every attachment
and marks the ones that are not text, pointing at `view_image` /
`transcribe_audio`. Reads keep filtering; only the inventory stopped
lying. An omission in an inventory reads as "there is nothing else",
which is the one thing it must never imply falsely.

**Rendered documents are redacted.** `.ovpn` and `.conf` are in scope
now, and those carry key material. `export.Redact` (ADR 077) runs before
truncation — a cap that bisects a private key still spills half of one —
and the count is reported so a redaction is never silent.

## Consequences

- Logs, configs and dumps are readable without leaving the session; this
  closed a real analysis gap the same day it was found.
- The extension list is an allow-list and will need occasional additions.
  That is the intended failure mode: an unlisted extension is skipped
  and *visible in the listing*, so the gap is discoverable rather than
  silent.
- Redaction is shape-based. Prose secrets in a config comment are not
  matched, exactly as ADR 077 reasoned for the corpus.
- Classification, extension parsing, listing and redaction are pure and
  unit-tested with no API in the loop.

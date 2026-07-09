# ADR 041: `view_image` — see image attachments inline

Date: 2026-07-09
Status: accepted

## Context

Slack messages carry screenshots, photos, and business cards, but the
model had no way to *see* them — digests rendered `[🖼 file.jpg]` and
stopped there. The same week, a partner sent a photographed vendor card
whose entire content (who to contact, what they sell) was in the image;
the assistant could only guess. Voice notes already had
`transcribe_audio`; pictures needed the visual equivalent.

## Decision

Add `view_image` (permalink, or channel + timestamp; workspace-aware)
that downloads a message's `image/*` attachments with the server's own
token and returns them as **inline MCP `ImageContent`** (base64 +
mimetype) so the model sees them directly in the tool result — not a
path it has to open separately. This is more capable and more portable
than a download-only tool: any MCP client with vision, not just one
with a local file reader, gets the picture.

Design choices:
- **Inline, with a size cap.** base64 inflates ~33% and hosts bound
  result size, so an image over `maxInlineImageBytes` (6 MB) is NOT
  inlined — its temp file is kept and the path returned, and the caller
  reads/downscales it on its own terms. Under the cap, the temp file is
  removed once its bytes are in the response.
- **Self-describing result.** A leading text block summarises count,
  sizes, and any skipped non-image files, so the result reads sensibly
  even before (or without) the images rendering.
- **Reuse, don't duplicate.** The audio download plumbing was already a
  predicate-driven `fetchFiles` / `downloadFiles` / `confinedTempPath`
  chain; this ADR renames those from their `*Audio*` names to
  attachment-neutral ones and threads a temp-filename `prefix`
  (`slk-audio` vs `slk-image`) so audio filenames stay byte-identical
  while images get their own. `view_image` is then a thin front end +
  the base64 encode.

## Consequences

- A partner's photo/card/screenshot is one tool call from being read
  by the model; digests that surface `[🖼 …]` now have a follow-up.
- No new security surface: the download source is still a Slack-resolved
  file object (not a caller URL), and the write path is the same
  confined temp path (ADR 040) — `view_image` only adds a read-back of a
  file the server itself just wrote.
- The `*Audio*` → neutral rename touches audio.go / transcribe.go and
  their tests; behaviour and audio temp filenames are unchanged.
- Additive tool ⇒ minor release (1.3.0) under the ADR 037 contract.
  520 → 524 tests.

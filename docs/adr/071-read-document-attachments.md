# ADR 071: read text attachments, and stop rejecting HTML as a scope error

Date: 2026-08-10
Status: accepted

## Context

A colleague attached two exported HTML documents and asked "tell me what
you think". Nothing in the server could open them. `view_image` handles
pictures, `transcribe_audio` handles sound, `download_audio` refuses
anything that is not audio or video — a document, which is how decisions
actually circulate, was the one attachment type with no reader at all.
The honest answer had to be "I did not read the files", which is the
worst possible answer to a request for review.

The obvious fix (widen the accept predicate) does not work on its own.
`downloadFiles` treats any download whose first bytes are HTML as proof
that the token lacks `files:read`, because Slack serves its sign-in page
with HTTP 200. That guard is exactly right for audio, video and images,
and exactly wrong for an `.html` attachment: the file would be rejected
as a scope failure for being what it is.

## Decision

**1. New tool `read_document`.** Resolves an attachment through the same
front half as the audio tools (permalink, file URL, channel + timestamp,
or latest-in-conversation with an optional `from` filter), renders it as
text, and returns it inline. Accepts `text/*`, the textual
`application/*` types, and — because Slack snippets often ship with an
empty mimetype — a known `filetype`. Unlike `download_audio`, the temp
files are removed before returning: nothing here needs to outlive the
call.

**2. The sign-in guard becomes type-aware.** For attachments that are
supposed to contain text, "looks like HTML" is replaced by "looks like
Slack's sign-in page": an HTML doctype *plus* a login marker. Binary
attachments keep the original, stricter rule. A scope failure is still
caught for both, and a real document is no longer mistaken for one.

Truncation is explicit (`max_chars`, default 40 000, reported in the
output) because a silent flood reads as a complete document.

## Consequences

- Proposals, specs, exports and snippets are readable in-session; a
  review request no longer requires leaving the tool.
- HTML is flattened rather than parsed: comments, `<script>` and
  `<style>` are dropped whole, block closers become newlines, entities
  are decoded. Layout and tables lose their shape — this reads documents,
  it does not render them.
- The sign-in heuristic for text is weaker than the binary one by
  construction. If Slack changes its login page wording, a document
  request under a scope-less token returns the login page as content
  instead of an error. The markers are listed in one place to keep that
  cheap to fix.
- 607 → 614 tests. Minor release (1.28.0).

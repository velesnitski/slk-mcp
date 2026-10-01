# ADR 114: links shared across organisations, and pages that build themselves

Date: 2026-10-01
Status: accepted

## Context

A colleague posted an HTML report into a group DM. `read_document` with
the file link failed with `file_not_found`, and when the right workspace
was named explicitly the "text" it returned was 146 KB of JavaScript and
none of the report. Two independent defects, plus a missing escape hatch.

**Routing.** ADR 110 routes a permalink by its host: the workspace whose
`auth.test` URL matches the link's host is the one that can read it.
Slack Connect breaks the assumption. A file or channel shared from
another organisation keeps that organisation's host, while only the
member workspace's token can read it. No configured workspace matches,
the call falls back to the primary, and the primary cannot see the
object.

**HTML conversion.** `htmlToText` dropped scripts and styles with one
expression, `<(script|style)…>.*?</(script|style)>`. Go's RE2 has no
back-references, so the closer was not tied to the opener: a script that
contains the string `</style>` (common in bundles that build a
stylesheet) ended there, and the rest of the bundle leaked out as text.
Separately, a self-contained page keeps its content outside the markup —
in a non-executable `<script type="application/json">` block or a long
`data-*` attribute — and those were dropped with the code.

**No escape hatch.** When conversion is wrong there was no way to get the
file itself; the caller had to ask a human to download it.

## Decision

1. **Fallback across workspaces, only when routing fell back.** If the
   link's host matches no configured workspace and the primary answers
   "not found", the remaining workspaces are tried in order. The first
   one that can see the object wins; if a workspace sees it but fails for
   another reason, that error is returned instead of the primary's
   not-found. An explicit `workspace` and a matched host are never
   retried elsewhere. Applies to `get_message` and the five file tools.
   A failure names every workspace tried.
2. **Separate expressions for `<script>` and `<style>`.** Executable
   scripts (no type, or any JavaScript dialect) are dropped; any other
   type is data and is kept as an `[embedded <type>]` block. `data-*`
   attribute values of 200+ characters are kept as well, base64-decoded
   when they decode to valid UTF-8. A page with almost no static text but
   more than 20 KB of script is reported as a JavaScript shell, with a
   pointer to `keep_file`, instead of returning a blank.
3. **`read_document keep_file`.** Saves the attachment unchanged to a
   temp file and returns its path. Not redacted — it never leaves the
   local machine, the same contract as `download_audio`.

## Consequences

- Links to content shared from another organisation resolve without the
  caller knowing which workspace is the member one.
- A not-found for an unmatched host costs one extra call per remaining
  workspace. Rate-limit and auth errors are not retried: they would fail
  the same way everywhere.
- Ordinary pages render exactly as before; the guard test pins that.
- The script-type list is an allow-list of code. An unknown type is
  treated as data, so the failure mode is showing a payload, not hiding
  one.

# ADR 040: confine download_audio write paths (defence-in-depth)

Date: 2026-07-09
Status: accepted

## Context

A path-traversal advisory in a sibling MCP server (yt-mcp,
GHSA-99mq-fjjc-6v9j: caller-influenced file path → arbitrary read +
exfiltration) prompted an audit of slk-mcp's filesystem surface. No
exploitable equivalent exists here:

- `download_audio` writes (never returns arbitrary local files); its
  download source is a Slack file object resolved from a real message,
  not a caller-supplied URL — so no SSRF / arbitrary read.
- `transcribe_audio` shells out via `exec.Command` (no shell), fixed
  flags, server-generated absolute paths, operator-controlled binaries.
- No tool combines a local read with an egress channel.

But one theoretical gap matched the flagged class: the temp write path

    filepath.Join(destDir, fmt.Sprintf("slk-audio-%s-%s", f.ID, sanitizeFilename(f.Name)))

sanitized `f.Name` but interpolated the Slack file `f.ID` raw. Slack IDs
are server-assigned safe tokens today, and `destDir` is never exposed to
the caller (always `os.TempDir()`), so there is no live traversal — but
`filepath.Join` cleans its result, so a slash or `..` reaching `f.ID` via
any future format change could escape the temp dir.

## Decision

`confinedAudioPath(destDir, fileID, fileName)` centralises the write-path
construction and makes confinement explicit — the same shape as the
yt-mcp ADR-027 fix:

1. Both untrusted inputs (`fileID` AND `fileName`) go through
   `sanitizeFilename`, so no separator or `..` survives into the joined
   name.
2. A final `filepath.Rel(destDir, path)` check rejects any result that
   escapes `destDir` (`..`, `../…`, or absolute), returning an error
   instead of writing.

This is defence-in-depth, not a fix for a live exploit — but it makes the
invariant ("a download never writes outside the temp dir") enforced by
code and pinned by a test, rather than resting on Slack's ID format.

## Consequences

- The write is provably confined regardless of what Slack puts in a file
  object; a hostile ID/name now yields a sanitized single component or a
  refusal, never a traversal.
- Patch release (1.2.1) — no API or behaviour change for well-formed
  inputs; the temp filenames are byte-identical for real Slack files.
- 519 → 520 tests.

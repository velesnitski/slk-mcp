# ADR 135: the README lists every tool, and a test keeps it so

Date: 2026-11-11
Status: accepted

## Context

The README's tool tables and badge are the catalogue a new user reads
first. They had drifted: the badge said 24 while 28 tools were
registered, four tools (`list_users`, `export_conversations`,
`archive_channel`, `unarchive_channel`) were missing from the tables,
and some descriptions no longer matched the behaviour — the unread
summary was "sorted by volume", and drill-in tools were said to work
on the primary workspace only.

## Decision

- Bring the README up to date: the four tools, the badge, the unread
  summary's ordering and delta cursor, per-call `workspace` on drill-in
  tools, the full `SLACK_READ_ONLY` list, and the whisper CPU fallback
  (ADR 134).
- `TestREADME_ListsEveryTool` reads every `mcp.NewTool("…")` name in
  `internal/tools` and fails when the README does not mention one in
  backticks, or when the badge count differs from the number of tools.

## Consequences

- Adding a tool without documenting it fails CI, which is where the
  drift would otherwise start.
- The test checks presence and the count, not the wording: a stale
  description still needs a reviewer.

# ADR 135: the README lists every tool, and a test keeps it so

Date: 2026-11-11
Status: accepted

## Context

The README's tool tables and badge are the catalogue a new user reads
first. They had drifted: the badge said 24 while 33 tools were
registered, five tools (`list_users`, `export_conversations`,
`archive_channel`, `unarchive_channel`, `get_list_items`) were missing
from the tables,
and some descriptions no longer matched the behaviour — the unread
summary was "sorted by volume", and drill-in tools were said to work
on the primary workspace only.

## Decision

- Bring the README up to date: the five tools, the badge, the unread
  summary's ordering and delta cursor, per-call `workspace` on drill-in
  tools, the full `SLACK_READ_ONLY` list, and the whisper CPU fallback
  (ADR 134).
- `TestREADME_ListsEveryTool` builds the server and asks it for its
  tools (`tools/list`, as a client would), then fails when the README
  does not mention one in backticks, or when the badge count differs.
  A first version scanned the source for `mcp.NewTool("…")` and missed
  the five tools registered through a variable name; the server's own
  list cannot miss one.

## Consequences

- Adding a tool without documenting it fails CI, which is where the
  drift would otherwise start.
- The test checks presence and the count, not the wording: a stale
  description still needs a reviewer.

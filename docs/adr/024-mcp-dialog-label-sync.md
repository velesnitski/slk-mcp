# ADR 024 — keep the /mcp dialog label in sync with the built version

**Status:** accepted
**Date:** 2026-06-17
**Tag at acceptance:** v0.5.0 (tooling only — no binary/behaviour change)

## Context

ADR 017 set `serverInfo.name` to `"slack v"+version` so the running
version is visible to MCP hosts. We assumed that also surfaced the
version in Claude Code's `/mcp` dialog.

It does not. The `/mcp` dialog labels each server by its **config key**
— the key under `mcpServers` in `~/.claude.json` — *not* by the
`serverInfo.name` the server returns during `initialize`. With the key
set to `"slack"`, the dialog shows `slack · ✔ connected · 18 tools`; the
`"slack v0.5.0"` we report in `serverInfo.name` only appears in the
server-instructions header and in hosts that render `serverInfo.name`.

Another server in the same config "shows its version" in the dialog only
because someone hand-typed it into the config key. That key does not
track the binary: bump the binary and the dialog keeps showing the old
version until the JSON is hand-edited. Copying that approach here would
just propagate a label that silently lies after the next release.

## Decision

Treat the two surfaces as what they are, and automate the one that can't
self-report:

- **`serverInfo.name`** stays `"slack v"+version` (ADR 017) — the
  truthful, auto-updating channel for instructions / capable hosts.
- **The `/mcp` dialog label** is driven by the config key, which only a
  side process can change. `scripts/sync-mcp-label.py` finds the slk-mcp
  entry in `~/.claude.json` **by its binary path** (not by the key, which
  may already carry a version), runs that exact binary with `-version`,
  and renames the key to `slack v<version>`. It is idempotent, writes
  atomically (temp file + `os.replace`), and keeps a `.bak`.
- A `Makefile` wires it in: `make install` = `build` + `sync-label`.
  `sync-label` is deliberately **not** part of `build` — a plain compile
  must never mutate `~/.claude.json`; the sync runs only when you intend
  to reconnect.

We explicitly reject hardcoding the version in the config key: it cannot
auto-update and is correct only by coincidence between releases.

## Consequences

- After `make install` + a **Claude Code restart**, the dialog shows
  `slack v<version>` and it tracks the binary across bumps — no manual
  re-keying. A `/mcp` *reconnect* is NOT enough: the dialog reads the
  config key at **session start**, so reconnect (which only re-runs the
  server process) keeps the old label — confirmed empirically on v0.5.0.
- `sync-label` keys off the binary path, so it works whether the current
  key is `"slack"` or an older `"slack v0.4.x"`.
- The script rewrites `~/.claude.json` pretty-printed; that is a larger
  textual diff than the original (Claude Code stores it compact) but is
  semantically identical — JSON parsing is format-agnostic.
- Minor race: if Claude Code rewrites `~/.claude.json` from its in-memory
  copy *after* the sync, it can revert the key. In practice the sync is
  run just before a restart, so the window is small; the `.bak` is the
  safety net.
- The pattern is portable to the rest of the fleet (zbbx/yt/gl) — same
  script shape, different binary-match fragment and label.

## Validation

- `make build` stamps the version (`./slk-mcp -version` → `0.5.0`);
  `make test` green.
- `make sync-label` renamed the live key `"slack"` → `"slack v0.5.0"`
  (verified by reading back `~/.claude.json`); a second run is a no-op
  (`= already 'slack v0.5.0'`).

## Out of scope

- Build-time git SHA / dirty flag in `serverInfo.name` (proposed
  separately) — orthogonal; lands in the name, not the dialog.
- Auto-running sync on every `go build` — rejected to avoid touching
  `~/.claude.json` on unrelated builds and racing Claude Code's writer.

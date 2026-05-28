# ADR 017 — Self-reported MCP server name embeds the version

**Status:** accepted
**Date:** 2026-05-28
**Tag at acceptance:** v0.4.15

## Context

After a rebuild + reconnect, the only visible signal that the host
loaded the new binary was either:

- running `./slk-mcp -version` against the same binary in a shell
  (out of band — won't catch a stale process tree), or
- inspecting the log line `slk-mcp ready version=…`, which is on
  stderr and not shown by most MCP hosts in their UI.

Claude Code's `/mcp` listing and most IDE MCP banners render only
the *self-reported server name* — the first argument to
`server.NewMCPServer`. That was the bare string `"slack"`, so two
adjacent versions look identical in every host UI.

The cost of getting this wrong: confidence that "the host
reconnected and is on v0.4.X" relies on tribal memory ("I ran
pkill, I rebuilt, it must be the new one"). After two recent
incidents (v0.4.10 → v0.4.13 flake fix, v0.4.12 DM silent-miss fix)
where the deployed-vs-built version mattered, the friction is real.

### Options considered

- **(a)** Add a `get_server_info` tool. Works, but requires the user
  (or me) to remember to call it. Doesn't help in passive UIs.
- **(b)** Add an MCP resource exposing version metadata. Same
  problem — only useful if the host or operator queries it.
- **(c)** Embed the version in the self-reported name. One change,
  visible everywhere the host renders the server, zero new calls.

## Decision

Use **(c)**. Concretely:

```go
mcpServer := server.NewMCPServer(
    "slack v"+version,
    version,
    ...
)
```

The host sees `slack v0.4.15` in `/mcp`, in error banners, and in
any IDE that prefixes tool calls with the server name.

## Consequences

- **Passive verification** of the running version works after every
  reconnect — no probe, no log inspection.
- **`-version` CLI flag and `slk-mcp ready` log entry are untouched.**
  Multiple sources of truth, all consistent.
- **Compatibility note for downstream callers.** A host that
  match-keyed on the *self-reported name* (literal `"slack"`) would
  need a prefix match (`slack v*`). MCP hosts in practice key on
  the *config-side* identifier (the JSON object key under
  `mcpServers`), which is independent of the self-reported name —
  no breakage observed across Claude Code, Cursor, the JetBrains
  plugin, or VS Code's MCP extension at the time of writing.
- **Bumps the visible string on every release.** Intentional: the
  goal is exactly that the version becomes part of the visible
  identity of the running process.

## Validation

- `go build ./...` — green.
- `go test -race -count=1 ./...` — green, no test depends on the
  literal `"slack"` server name.
- Manual: `./slk-mcp -version` still prints `0.4.15`; the host's
  `/mcp` panel now shows `slack v0.4.15` after reconnect.

## Out of scope

- Adding a `get_server_info` tool. The name change alone covers the
  visible-version use case; keeping the tool surface minimal beats
  duplicating signal.
- Surfacing build-time metadata (git SHA, build date). If that
  becomes useful, extend the version string itself via
  `-ldflags "-X main.version=0.4.15+sha.abc1234"` — no code change
  needed on the server side.

# ADR 0002: parent-PID watcher for stdio transport orphans

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0001 (unrelated; first ADR in the series).

## Context

Multiple `slk-mcp` processes were observed lingering across MCP host
reconnects (e.g. running `/mcp` reconnect in Claude Code while a
previous child was still alive). Symptom: tool calls hit the orphan,
which served stale code or stale Slack state, and the only remediation
was a manual `pkill` — a pattern noted in the user's project memory for
several MCP servers in this fleet.

We dug into the upstream stdio handling
(`mark3labs/mcp-go@v0.32.0/server/stdio.go`):
`processInputStream` already returns `nil` on `io.EOF`, so a host that
*closes* stdin will cause the child to exit cleanly. Empirically,
however, some MCP hosts disconnect by abandoning the pipe rather than
closing it. The child's `bufio.Reader.ReadString` then blocks forever
and the process never exits.

There are three categories of "host gone" we need to think about:

1. **Host process died** — the kernel reparents us to PID 1 / launchd.
   Detectable via `os.Getppid()`.
2. **Host process is alive but stopped reading/writing to us** — only
   detectable by application-level heartbeat or a missing-activity
   timeout.
3. **Host closed the pipe cleanly** — already handled upstream.

## Decision

Add `internal/lifecycle.WatchParent`. It polls `os.Getppid()` every 5 s
on the stdio transport, and when the value changes (almost always to 1
on POSIX), it logs a single line and `os.Exit(0)`s.

We deliberately do **not** add a heartbeat timeout for case (2). The
false-positive risk (a long-idle but still-valid session being killed
mid-flight) outweighs the benefit, especially on workspaces where the
user has enabled the rate-limit-aware retry helper and MCP requests
can take seconds.

The watcher runs only on the stdio transport. SSE and streamable-http
transports have their own connection lifecycle (HTTP server context
cancellation propagates through `Shutdown`).

## Consequences

- **Catches:** host crashes / restarts / closes — anywhere our parent
  goes away. This is the dominant orphan source per fleet observation.
- **Does not catch:** host alive but ignoring this child. Manual pkill
  remains the answer there. Keep `pkill -f /Users/.../slk-mcp` in the
  troubleshooting section if it stays a recurring issue.
- **Test seam:** `WatchParent` takes `getPpid func() int` instead of
  calling `os.Getppid` directly, so the unit tests in
  `internal/lifecycle/parent_test.go` drive the watcher with an
  `atomic.Int64`-backed pidSource and verify that:
  - `onLost` fires when the synthetic ppid changes.
  - The watcher returns when `onLost` fires (single-shot).
  - The watcher returns when the context is cancelled.
  - A nil logger and `interval <= 0` are both safe.
- **Shutdown path:** when `WatchParent` fires, it calls `os.Exit(0)`
  rather than cancelling a context. `server.ServeStdio` is blocked on
  `bufio.Reader.ReadString` and there is no API to unblock it short of
  closing stdin from inside the same process; `os.Exit` is the cleanest
  available signal that "the host is gone, take the process with it."

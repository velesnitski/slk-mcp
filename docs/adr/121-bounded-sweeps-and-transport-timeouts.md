# ADR 121: bounded sweeps and transport timeouts

Date: 2026-10-15
Status: accepted

## Context

`get_unread_summary` and `get_mentions` regularly ran past two minutes,
long enough for an MCP host to move them to the background. Three
mechanisms could explain it, and nothing in the log said which:

- The Web API clients used slack-go's default `http.Client`, which has no
  timeout: a connection that is accepted and never answered blocks the
  call indefinitely.
- The retry helper waits whatever `Retry-After` Slack sends, up to five
  times, with no regard for how long the caller can wait.
- Both tools run their Slack-bound phases one after another, per
  workspace, bounded only by the caller's context.

## Decision

- **Transport bounds, not a client timeout.** Dialing (10 s), the TLS
  handshake (10 s) and the wait for response headers (30 s) are bounded on
  a clone of the default transport. `http.Client.Timeout` was rejected:
  it also caps reading the body, and the same clients download huddle
  recordings and large documents, which can legitimately stream for
  longer. A process that has replaced `http.DefaultTransport` keeps its
  transport; requests are routed through it rather than around it.
- **Deadline-aware rate-limit waits.** When the context has a deadline
  and the requested wait would outlast it, the helper returns the
  rate-limit error at once. Without a deadline nothing changes.
- **Sweep deadline.** `SLACK_SWEEP_TIMEOUT` (seconds, default 100, `0`
  off) bounds the fetch phases of both tools across all workspaces. The
  default sits below the common 120 s background threshold. Rendering
  runs on the caller's context so names still resolve after a cut.
- **Cut phases are named, never hidden.** A phase that is skipped, or
  fails because the deadline passed, is listed in an `incomplete:` line;
  a workspace not reached is listed as skipped. An empty mentions list
  from a cut sweep reports the cut, not "no mentions".
- **Cursor rule.** If any data phase of a workspace's unread sweep was
  cut, that workspace returns its incoming cursor. Advancing it would
  make the next delta pull skip what this pull never read; re-showing
  some messages is the accepted cost. Display-only phases — the
  answered-DM probe and the pending-reply check — refine what is shown
  but lose nothing when cut, so they never hold the cursor back.
- **Phase timing.** Each phase logs one INFO line with tool, workspace,
  phase and duration (and the error, if any), so the next slow sweep
  shows where the time went.

## Consequences

- A slow workspace degrades to a partial, labelled result instead of a
  call that never returns.
- Messages already shown may reappear on the next pull after a cut.
- Concurrency between phases was not added: per-phase timing should
  first show whether it would help or only hit rate limits sooner.

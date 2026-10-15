# ADR 122: concurrent workspaces, sweep exclusion, log file

Date: 2026-10-15
Status: accepted

## Context

After ADR 121 the sweeps were bounded but still slow: a delta
`get_unread_summary` across three workspaces took over a minute even when
almost nothing was new, and `get_mentions` about as long. Three things
stood in the way of fixing it well:

- The per-phase timings ADR 121 added went to stderr, which an MCP host
  does not keep, so they could not be read.
- Workspaces were swept one after another, though each has its own token
  and rate limits.
- Every configured workspace was swept, including ones the operator does
  not read in a routine digest.

The obvious speed-up — skipping a channel's history read when a delta
cursor shows no new top-level message — was considered and deferred: the
history page is also how already-read thread parents with new replies are
found (ADR 111). Skipping it would trade speed for silently missed thread
replies. It needs measurements first.

## Decision

- **Log file.** `SLACK_LOG_FILE` makes the logger append to a file as well
  as stderr. The file is created 0600 in a 0700 directory (logs name
  people and channels), a leading `~/` is expanded, and an oversized file
  (> 10 MB) is rotated to `.1` once at startup. If the file cannot be
  opened the server still starts and says why on stderr.
- **Sweep stats.** `UnreadAll` logs one line per pass: channels listed,
  history reads, channels with unread, failures, time to list, total time
  and the slowest channel. Each workspace logs its total sweep time.
- **Concurrent workspaces.** Both sweep tools run their workspaces in
  parallel and assemble results by registry index, so the rendered order
  and the combined cursor are exactly what the sequential loop produced.
  The shared deadline (ADR 121) now cuts all workspaces mid-sweep rather
  than skipping the later ones; each reports itself incomplete and keeps
  its incoming cursor, so the former "not started" branch was removed.
- **Exclusion.** `SLACK_SWEEP_EXCLUDE` lists workspace labels the two
  tools skip when no `workspace` is given. Naming a workspace always
  reaches it; excluding all of them is ignored. The output names the
  skipped workspaces, and their incoming cursor is carried into the
  combined token, so a later explicit pull resumes where it left off.

## Consequences

- Wall-clock time of a multi-workspace sweep becomes the slowest
  workspace, not the sum.
- The next optimisation can be chosen from logged numbers rather than
  guessed.
- A concurrency test pins the parallelism: it fails if the workspaces are
  swept one after another.

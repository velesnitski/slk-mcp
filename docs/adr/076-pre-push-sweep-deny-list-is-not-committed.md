# ADR 076: the pre-push guard's deny-list is not committed

Date: 2026-08-18
Status: accepted

## Context

This repository is public. `CLAUDE.md` has always required generic
placeholders in tests and docs — but a rule that lives only in prose is
enforced only by whoever happens to remember it while writing the line.

Placeholders drift toward whatever the author had in front of them at the
time. A fixture reads as scaffolding rather than as published data, and
that mismatch between how it feels to write and what it actually is
survives review far better than a leak in an obvious place would.

## Decision

Add `scripts/sweep.sh`, and split its patterns into two sources.

**Credential shapes ship inside the script.** `xox[bpasr]-`, `glpat-`,
`gh[pousr]_`, `AKIA…`, PEM headers. These are safe to publish because
each describes a *format*, never a value. They are deliberately written
to require real-token entropy so the repo's own placeholders
(`xoxb-test`, `xoxp-secondary`) don't trip them — a guard that cries wolf
on its own fixtures gets switched off within a week.

**Deployment-specific strings live in `.sweep-patterns.local`, which is
gitignored.** Workspace labels, channel names, hostnames, internal
product and ticket codes. This is the load-bearing half of the decision:
a committed deny-list *publishes the exact strings it exists to keep out*,
and does so in a file whose whole purpose announces that they are
sensitive. The guard must not be capable of leaking what it guards, so
the sensitive half never enters the object store.

The trade is discoverability: a fresh clone gets a guard that only knows
credential shapes. It says so on stderr rather than reporting a clean
sweep it did not perform.

`--history` scans commit messages and the object store as two separate
passes, because a string removed from the working tree survives in both,
and the two are scrubbed by different mechanisms.

## Consequences

- `make sweep` before pushing; `make sweep-history` before publishing a
  repo or after any rewrite.
- Advisory, not CI. A CI runner has no `.sweep-patterns.local`, so a CI
  job would silently degrade to credential shapes only and report green —
  a guard that passes for the wrong reason is worse than no guard. This
  runs locally, where the deny-list exists.
- Matching uses `perl`, not `grep`: BSD `grep` has no `-P`, and only
  `git grep` carries PCRE — which does not help when the input is a
  stream of objects rather than a tree.
- Exit code is the interface (0 clean / 1 hits), so it composes into a
  pre-push hook without parsing output.

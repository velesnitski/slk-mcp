# ADR 085: the pre-push sweep scans what is being pushed, not all of history

Date: 2026-09-01
Status: accepted

Amends ADR 083, which specified `--history` for the pre-push hook.

## Context

ADR 083 put the full deny-list scan in a `pre-push` hook and ran it with
`--history`, reasoning that a string committed earlier in a branch and
removed before pushing still ships in a reachable blob. That reasoning
holds. The scope did not.

`--history` scans everything reachable from every ref — including commits
published months ago. The first time a deny-list pattern was added for a
string that already existed in old objects, the hook began refusing
**every** push, for content it was powerless to affect. Refusing today's
push cannot un-publish something that went out a year ago.

That leaves exactly two exits, and both are worse than the leak:
`--no-verify`, which trains the habit ADR 082 closed on purpose, or
deleting patterns from the deny-list, which weakens the guard to make it
quiet. A guard whose only escape is to disable it will be disabled.

## Decision

**The hook scans the working tree plus the commits actually being
pushed.** Git already hands a `pre-push` hook the exact range on stdin
(`<local ref> <local sha> <remote ref> <remote sha>`); the hook now reads
it and passes it to `sweep.sh --range`. A branch with no remote
counterpart is bounded by `--not --remotes` rather than walked back to
the project's first commit. Branch deletions are skipped.

`sweep.sh --history` is unchanged and still audits the entire object
store. That is the right scope for a different question — before making
a repo public, or verifying a rewrite — and it is what `make
sweep-history` runs.

## Consequences

- The hook is now answerable: it blocks only what the developer can
  actually fix, which is what makes it survivable as a default.
- Pre-existing published strings stop blocking work and become what they
  are: a separate, scheduled cleanup, visible via `make sweep-history`
  rather than as a wall in front of every push.
- The deny-list can be as complete as we like — it lives only on this
  machine, so adding a pattern costs nothing and no longer risks
  bricking pushes.
- Verified both directions: a planted banned string inside a pushed
  commit blocks the push; the same string present only in already-
  published history does not.

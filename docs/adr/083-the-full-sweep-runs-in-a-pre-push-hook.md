# ADR 083: the full sweep runs in a pre-push hook, and scans reachable objects only

Date: 2026-08-31
Status: accepted

## Context

CI runs the sweep in `--shapes-only` mode and says so, because the
deny-list is deliberately untracked (ADR 076). Closing that gap by
putting the list in a GitHub Actions secret was considered and rejected:

- This repository is public. A secret there is protected by exactly one
  thing — that every future workflow is written correctly. One
  `echo "$SWEEP_PATTERNS" | base64` publishes the list into a public
  Actions log that no history rewrite can reach. Masking does not stop
  that.
- The list's contents *are* the sensitive part. It enumerates the
  strings we consider sensitive, which is the meta-leak ADR 076 declined
  to publish. A secret is the same file, deferred by one mistake.

So the full scan belongs on the machine that already holds the list. The
remaining problem is that "run `make sweep` before pushing" is a human
guarantee, and CI exists because humans forget.

## Decision

**A tracked `pre-push` hook**, installed with `make hooks`
(`core.hooksPath = .githooks`). Tracked so it is reviewed like code;
what it reads stays untracked. The deny-list never leaves the machine.

**It runs `--history`, not just the tree.** A string committed earlier in
a branch and removed before pushing still sits in a blob that the push
publishes, and the tree scan cannot see it. That is the realistic
mistake. Measured cost: 2.7s against 0.65s for the tree alone.

**No quiet override.** `git push --no-verify` is the escape and it is
visible in shell history. An env-var bypass would be the same escape
hatch ADR 082 just closed.

**The history scan walks reachable objects only.** This was found by the
hook's own negative test, before it shipped: `--batch-all-objects` reads
unreachable objects too, so staging a secret and then correctly
unstaging it leaves a dangling blob that blocks *every* push until gc
runs two weeks later — pointing at a file that no longer exists. The
developer does the right thing and the guard still refuses. That is how
a guard teaches people to reach for `--no-verify`. `git rev-list
--objects --all` enumerates exactly what a push can publish.

## Consequences

- Full deny-list coverage on every push, with the list never leaving the
  machine or entering a third-party runner.
- CI stays `--shapes-only` and keeps saying so. It is a backstop for
  credential shapes, not a claim of full coverage.
- A fresh clone has no hook until `make hooks`. This is the honest cost
  of not shipping the list to CI; it is one command, named in CLAUDE.md.
- `--no-verify` still bypasses everything. The hook raises the floor; it
  is not a control against someone who means to skip it.
- Verified both ways: a credential shape in a staged file blocks the
  push, and a shape committed then removed from the tree is still caught
  by the history scan.

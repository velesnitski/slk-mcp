# ADR 079: the sweep fails closed, and can therefore run in CI

Date: 2026-08-19
Status: accepted
Supersedes the CI consequence of ADR 076

## Context

ADR 076 concluded that the sweep must stay local, because a CI runner has
no `.sweep-patterns.local` and would "silently degrade to credential
shapes only and report green".

That was a true observation about the script as written, and the wrong
conclusion drawn from it. The script degraded silently because it was
built to; nothing about CI required that. The fleet's original sweep had already
made the other choice: a missing pattern file is **exit 2**, not a pass.

Failing open is the worst property a guard can have, and it fails open in
its single most common failure mode — the fresh clone.

## Decision

Adopt the established contract from the fleet's original sweep:

- A missing or empty pattern file **exits 2**. `--shapes-only` is the
  explicit, named way to ask for the reduced scan.
- `.sweep-patterns.example` is tracked; the template carries only invented
  strings and is excluded from the scan, since it would match itself.
- `CS:` prefixes a case-sensitive pattern; plain lines are
  case-insensitive. Uppercase ticket codes need the former, and a blanket
  case-insensitive match clobbers lowercase words sharing the prefix.
- Patterns use `\b`, never lookaround: the tree is scanned by `grep -E`
  and the history by `perl`, and only `\b` means the same thing in both.
- Success prints the tracked-file count. A scan of nothing is visible.
- Matching goes through `git ls-files` piped to plain `grep`, not
  `git grep`, which has been seen to return nothing under a wrapper — and
  a scan returning nothing is indistinguishable from a clean one.

Two things this repo adds on top: `--history`
(commit messages and the object store, scanned separately, because a
string scrubbed from the tree survives in both) and the `sweep:allow`
marker, now **partial** — it exempts a line from the shape rules only,
never from the deny-list, or it would become the way to smuggle an
identifier past everything.

`--quiet` reports `file:line` and withholds the matched text. It is not a
convenience: this repo is public, so its Actions logs are public, and a
guard that prints the offending string publishes the very thing it exists
to catch — into a log no history rewrite reaches.

## Consequences

- CI gains a sweep job. With the `SWEEP_PATTERNS` secret set it runs the
  full scan over full history; without it, the shapes-only scan, and it
  announces which. Neither variant can report a pass it did not earn.
- The job is not in the required-checks list yet — that is a branch
  protection change, made deliberately and separately.
- A fresh clone gets exit 2 until the operator recreates the deny-list.
  That is the intended experience: an absent list is a setup step, not a
  silent downgrade.

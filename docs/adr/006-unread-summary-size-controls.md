# ADR 006 — `get_unread_summary` size controls (`max_chars`, `skip_log_mode`, `skip_git_mode`)

**Status:** accepted
**Date:** 2026-05-15
**Tag at acceptance:** v0.4.4

## Context

`get_unread_summary` is the top-level entry point for daily-recap
LLM consumers. The handler:

1. Fetches unread state for every joined channel (`UnreadAll`).
2. Ranks channels by urgency (`digest.RankUnread` — composition of
   mention-count, age decay, keyword hits).
3. Renders each ranked channel inline into a single Markdown body,
   with detector-based compression for git / log / low-signal
   channels.

On a workspace with ~45 unread channels the rendered body crossed
**55,000 characters** — past the per-tool result token cap our LLM
consumer enforces. The wrapper then dumped the result to a sidecar
file with instructions to read it in chunks; the LLM ended up
shelling out to `python3` and `grep` to slice the JSON, defeating
the point of the tool.

The diagnostic insight: the ranking already knew which channels
mattered most. It just wasn't being used as a *budget filter*, only
as an *ordering signal*. Anything past the rank-1 channel was
inlined unconditionally.

A secondary observation: log/git-mode channels (~12 + 8 in the
overflowing workspace) consumed roughly half the byte budget despite
being almost-pure bot noise. The user reading the recap habitually
skipped them.

### Options considered

- **a.** Cursor-style pagination (`cursor`, `page_size`). Forces the
  consumer to manage state across calls; awkward for an LLM that
  prefers single-shot tools.
- **b.** Tiered response: first call returns channel headers + counts,
  caller drills in via `get_channel_digest`. Breaks one-call
  ergonomics for the common case.
- **c.** Hard-coded server-side max with no caller override. Trades
  one set of broken workflows for another.
- **d.** Three additive optional parameters:
  - `max_chars` (soft cap, default `0` = unlimited),
  - `skip_log_mode` (skip log-mode channels entirely),
  - `skip_git_mode` (skip git-mode channels entirely),
  plus lowering the default for `log_samples_per_band` from `3` to
  `1` (INFO-band sample messages rarely added signal).

## Decision

Use **(d)**. Each parameter is additive, optional, and defaults to
the previous behaviour (`max_chars=0` is the only flag that *changes*
behaviour, and only when set), so existing callers see no diff. The
default for `log_samples_per_band` is a deliberate breaking change to
the *internal compression* of one rendering mode — the contract is
unchanged, the byte cost drops.

The cap is enforced through a tiny `budgetAppend` helper that wraps
the buffer writes. It returns `false` when a write would push the
buffer past `maxChars`; the handler accumulates rejected labels and
emits them as a footer:

```
+ 18 channels omitted by max_chars cap: #foo, #bar, …
  (use get_channel_digest to drill in)
```

The loop uses `continue`, not `break`, after a rejection. Channels
are scanned in urgency order but vary widely in size — a 4KB log
channel ahead of a 600-byte real-conversation channel should not
starve the latter just because it sorted first.

## Consequences

- LLM consumers gain a knob (`max_chars`) tuned to whatever token
  cap their wrapper enforces. The signal stays — only the
  long-tail noise is dropped.
- The dropped-channels footer turns a hard truncation into a
  pointer to the right follow-up (`get_channel_digest`). Lossy by
  design, but discoverable.
- `skip_log_mode` / `skip_git_mode` are pragmatic shortcuts: a
  caller who never reads bot feeds can skip them in one flag rather
  than calibrating `max_chars`. Both flags compose with `max_chars`
  freely.
- `log_samples_per_band` default change reduces output for every
  caller that didn't explicitly set it. The samples for INFO bands
  rarely carried signal; the severity counts (always rendered) do.
- The ranking layer (`digest.RankUnread`) is now load-bearing for
  *budget decisions*, not just ordering. If the ranker silently
  regresses, the truncation footer will surface the wrong channels.
  Existing rank tests (`internal/digest/rank_test.go`,
  `internal/tools/unread_helpers_test.go`) guard against that.

## Validation

- `TestBudgetAppend_*` in `internal/tools/unread_budget_test.go`:
  unlimited-cap always writes; under-budget writes succeed;
  over-budget writes are rejected without mutating the buffer;
  exact-fit writes succeed; a small channel still fits after a
  larger one was rejected (the `continue` invariant).
- `go test ./... -race` clean.
- Manual: with the original 55K-char workload, `max_chars=12000`
  yields a body around 11.5K + the dropped-channels footer.

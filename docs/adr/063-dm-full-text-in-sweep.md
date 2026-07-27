# ADR 063: fuller DM bodies in the unread sweep

Date: 2026-07-27
Status: accepted

## Context

get_unread_summary truncates every message body to the 280-char
MessageLineLimit preview. Right for channels (breadth over depth), wrong
for DMs: DMs are the actionable layer — few, human-length, and the place
amounts, deadlines and asks live. A billing DM ("оплаты хватит на N
дней… сумма…") rendered as "(+866 chars)", forcing a second
get_channel_digest(full_text) round-trip to recover the number that
mattered — repeatedly.

## Decision

Render DM channels in the sweep at a generous per-message cap while
channels stay compact.

- New `format.WithMessageLimit(n)` overrides the truncation length for a
  render (0 = default; WithFullText still wins). Threaded through
  `messageLineImpl`/`writeReplies` as an explicit limit param.
- The sweep applies `WithMessageLimit(1500)` to DM channels only
  (`slack.IsDirectMessage`), gated by `dm_full_text` (default true).
- **Bounded, not full_text on purpose:** an unbounded DM body could
  exceed the char budget and be dropped entirely by budgetAppend —
  hiding the very message it meant to surface. 1500 covers
  amounts/deadlines/short paragraphs; a genuine wall-of-text still
  truncates.

## Consequences

- The recurring "sweep truncated the one line that mattered" DM
  round-trip is gone; channel previews and the total budget are
  unchanged.
- Additive: `dm_full_text` defaults true, set false for a leaner
  uniformly-truncated sweep. WithMessageLimit is reusable by any digest
  caller.
- 589 → 591 tests (per-call limit: full-render, bounded-truncate,
  full_text-overrides). Minor release (1.20.0).

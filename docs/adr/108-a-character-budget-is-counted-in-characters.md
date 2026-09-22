# ADR 108: a character budget is counted in characters

Date: 2026-09-22
Status: accepted

## Context

Writing coverage for `internal/format` turned up three defects in code
that had no tests. All three were invisible in normal use, which is why
they survived.

**Two byte-sliced truncations.** `DecisionLine` capped a body with
`body[:160]` and `searchResultLine` with `body[:200]`. Both indices are
byte offsets. For ASCII the two agree, so every test and every English
channel looked correct. For any multi-byte script they do not: when the
cut lands inside a character, the result is invalid UTF-8 and the
reader gets U+FFFD where the text should be.

This is not an edge case for a body that reaches a truncation limit.
Cyrillic is two bytes per character, CJK three, emoji four — so a cap
expressed in bytes is reached at roughly half or a third of the
characters a reader expects, and whether the cut lands mid-character
depends on how many single-byte characters happen to precede it.
`searchResultLine` is on the hot path: it renders every
`search_messages` result.

The limits were also wrong as budgets. The point of the cap is to bound
what the reader receives, and a reader — human or model — consumes
characters, not bytes. Capping at 200 bytes gives a Latin message 200
characters and a Cyrillic one 100, silently, for no stated reason.

The package already had `truncateRunes`, added in ADR 103 for exactly
this problem in the hidden-payload marker. Two call sites simply
predated it.

**A filter that wrote to its caller's memory.** `LogChannelDigest`
dropped contentless patterns with the standard in-place idiom,
`nonEmpty := band.Patterns[:0]`. That slice shares a backing array with
the caller's, so rendering — a read — overwrote the bands it was handed:
surviving patterns were compacted to the front while the caller's length
stayed the same. A caller that renders twice, or inspects its bands
afterwards, sees duplicated entries. Nothing in the tree does that
today, so the bug was latent rather than active; the idiom is correct
when the function owns the slice, and this one does not.

## Decision

Truncation limits in this package are counted in runes and applied with
`truncateRunes`. `decisionBodyLimit` and `searchBodyLimit` are named
constants carrying that unit in their doc comment, so the next limit
added here starts from the right unit rather than rediscovering this.

The numbers are unchanged — 160 and 200 — which widens the budget for
non-Latin bodies. That is the intended reading of the limit, not a
regression: the cap always meant "this many characters", and only
ASCII made the two interpretations agree.

`LogChannelDigest` filters into a fresh slice. A renderer does not
write to its input.

Coverage of `internal/format` rises from 79.2% to 91.6%, with the
UTF-8 property asserted directly (`utf8.ValidString` plus an explicit
U+FFFD check) across Cyrillic, CJK and emoji bodies rather than through
a golden string that would pass for the wrong reason.

## Consequences

- Truncated non-Latin bodies render as text instead of ending in a
  replacement character, and carry the same number of characters a
  Latin body would.
- Output grows for non-Latin bodies that reach a cap — up to 2-4x for
  the truncated tail of one line. That is the correct budget, but it is
  a real increase in a token-sensitive path, and `full_text` remains
  the way to opt out of truncation entirely.
- A renderer mutating its argument is now covered by a test that reads
  as a property ("does not mutate caller's Patterns"), so the in-place
  idiom cannot come back unnoticed.
- The remaining uncovered statements in `internal/format` are
  defensive branches on malformed Slack payloads; they are reachable
  only by constructing values the API does not produce.

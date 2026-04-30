# ADR 0005: urgency heuristic for unread channel ranking

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0003 (mention markers), ADR 0004 (mentions_only filter).

## Context

`get_unread_summary` originally ranked channels by raw unread volume,
then by mention presence (ADR 0003). For a workspace with ~89
channels, raw volume puts noisy chats above quiet-but-urgent ones —
e.g. 30 status-update messages in a release channel outranked a
single "срочно сломалось" in a smaller incident channel. The
operator's manual triage was reordering the digest to find the real
urgency, which is exactly what the LLM-side ranking should be doing.

## Decision

Add `internal/tools/urgency.go`:

- **`urgencyScore(cu, now)`** — sums per-message bonuses across both
  top-level messages and thread replies.
- **`messageUrgency(m, now)`** — per-message score, four signals:
  - **Question marks** (ASCII `?` and full-width `？`), capped at 3
    per message × 2 = max 6.
  - **Urgency keywords** (case-insensitive substring) — English
    (`urgent`, `asap`, `blocker`, `critical`, `important`, `stuck`)
    and Russian (`срочно`, `критично`, `блокер`, `помоги`,
    `сломалось`, `упало`, `блокирует`, `не работает`, `горит`).
    Each unique keyword hit = +10. Entries are non-overlapping
    on purpose: `помогите` is omitted because it strictly contains
    `помоги` and would double-score any "помогите" message.
  - **Reactions** that humans typically use to flag importance
    (`rotating_light`, `siren`, `fire`, `warning`, `exclamation`,
    `bangbang`, `x`, `no_entry`) — +3 each.
  - **Recency** — +5 if posted in the last hour, +2 if in the last
    six hours. `now` is injected so tests pin recency
    deterministically; pass `time.Time{}` to disable the band.

`rankUnread` is now layered:

```
rank = volume + urgencyScore + (mention ? 1_000_000 : 0)
```

A direct mention still dominates any urgency in non-mention channels;
urgency dominates raw volume for channels with even moderate signal
(a single keyword outranks ~9 plain messages).

## Why this is heuristic, not classification

This is a ranking aid for an LLM that does the actual semantic work
afterwards. Misclassification is cheap: a false-positive urgent
channel surfaces a few seconds earlier in a digest the operator was
going to read anyway. The LLM still gets to apply real tone judgement
on the rendered output. We deliberately do *not* try to detect
sarcasm, ALL CAPS, exclamation chains, or sentiment — those need
real NLP and would over-fit on local patterns.

## Consequences

- One channel ordering change visible in `get_unread_summary` output.
  No new API calls, no new tool parameters, no new dependencies.
- Test coverage: `internal/tools/urgency_test.go` adds 14 new cases
  covering each signal, the recency bands, the keyword-overlap dedup
  invariant, and the ranking interaction (mention beats urgency,
  urgency beats raw volume). Tests use `fixedNow()` and `tsOffset()`
  helpers so recency is deterministic.
- Future: when we eventually want to expose tuning to operators,
  an `urgency_weight` parameter on `get_unread_summary` is the natural
  knob. Not added now — premature.

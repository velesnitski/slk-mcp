# ADR 059: answered-DM suppression in the unread sweep

Date: 2026-07-22
Status: accepted

## Context

Slack advances a conversation's `last_read` on **client focus**, not on
send. Reply to a DM from a notification (or without re-focusing the
conversation) and the DM stays "unread" server-side for minutes — the
sweep then re-surfaces the counterpart's questions as pending even
though the operator already answered them. Hit live: two DMs answered at
15:35–15:39 still rendered as "awaiting reply" in a 16:00 sweep, and the
operator reasonably asked "mcp bug?". Worse, the render showed only the
counterpart's messages, so the output carried no hint that an answer
already existed.

## Decision

Suppress answered DMs, verified against ground truth rather than
last_read.

- For each DM in the post-filter result set, probe the conversation's
  actual newest message (`conversations.history limit=1` — one tiny call
  per DM shown, immune to last_read lag and to fetch-vs-read races).
- `isAnsweredDM` (pure): DM + newest message authored by the operator →
  answered. `dropAnsweredDMs` splits kept/answered and **fails open** —
  a probe error keeps the channel (better to over-show than to hide a
  live question).
- Suppressed DMs collapse to one line: `N answered DM(s) hidden (you
  have the last word): @a, @b — pass show_answered=true to include`. If
  the whole sweep was answered DMs, that line replaces the bare "all
  caught up" so the information isn't silently lost.
- `show_answered=true` restores the old behaviour. Suppression is also
  skipped when `dm_window_hours > 0` — that mode explicitly requests
  already-read DM recaps, and fighting it would be wrong.

## Consequences

- The "already answered, but the sweep nags" class is closed — the third
  of the operator-visibility gaps (ADR 057 closed own-activity-in-
  threads, this closes own-replies-in-DMs).
- Cost: one `history limit=1` call per DM in the final result set (a
  handful per sweep), only when suppression is active.
- 581 → 584 tests (answered/not/nil/self-empty/non-DM, split +
  fail-open, no-probe-without-self). Minor release (1.16.0).

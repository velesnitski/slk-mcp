# ADR 051: surface new replies in threads you started or replied in

Date: 2026-07-16
Status: accepted

## Context

`get_unread_summary` missed a whole class of unread: **replies in a
thread the operator started (or already replied in) that don't
@-mention them**. Concretely — you post a request in a channel, a
colleague answers in the thread, and it never appears in the digest.

Why the existing passes miss it:
- **Slack itself** marks a channel unread only for new *top-level*
  messages. Thread replies land in the "Threads" view; the channel does
  **not** bold unless you're @-mentioned.
- **`UnreadAll.fetchReplies`** only fetches replies for parents that are
  in `cu.Messages` — i.e. newer than `last_read`. A thread *you*
  authored has an already-read parent, so its replies are never fetched.
- **`UnreadThreadMentions`** (`search to:me`) only catches replies that
  **@-mention** you. A colleague answering your own thread doesn't tag
  you, so this misses it too.

Net: replies to your own/participated threads without a mention fall
through every pass — a real silent miss (hit live on a due-diligence
thread the operator started).

## Decision

Add an **own-thread backstop**, mirroring the mention backstop but keyed
on authorship instead of mentions.

- `UnreadService.UnreadOwnThreads(hours)`: `search from:me after:<date>`
  discovers the (channel, thread-root) threads the operator is active in
  (a reply's root comes from its permalink `thread_ts`; a parent's is its
  own ts). For each, fetch `conversations.replies` and keep the messages
  from **others** posted **after the operator's last message in that
  thread** — via the pure, unit-tested `unseenAfterMine` (returns nil
  when the operator isn't actually a participant, so standalone messages
  and unrelated threads don't false-positive).
- New `own_thread_hours` arg on `get_unread_summary` (default **24**),
  merged with the existing `mergeThreadMentions` (augments channels,
  never replaces; dedups replies by ts).
- Needs `Self()` to distinguish the operator's messages; degrades to a
  no-op without a search backend or self id.

"After my last message" is the smart proxy for "unseen": if you already
replied at 14:59 and a colleague answered at 15:26, only the 15:26 reply
surfaces — not the whole thread you've been reading.

## Consequences

- A colleague answering a thread you started now shows up in the digest
  — the due-diligence case is caught. Distinct from the `to:me` pass
  (mentions) and complementary to it.
- Additive: `own_thread_hours=0` disables it; existing callers unaffected.
  Reuses `mergeThreadMentions`, so rendering/dedup are unchanged. One
  `search` call + one `conversations.replies` per active thread (bounded
  by the search page); best-effort per-thread so one failure doesn't sink
  the sweep.
- `UnreadClient` gains `UnreadOwnThreads`; no test fakes implement it, so
  no fakes to update. Minor release (1.10.0). 560 → 564 tests.

# ADR 102: `to:me` is not a mention

Date: 2026-09-17
Status: accepted

## Context

Two paths answer the question "who addressed me": `get_mentions`, and
the thread-mention backstop the unread sweep uses to catch replies in
threads whose parent is already read. Both built the same query:

    to:me after:<date>

on a premise this repo had written down as fact — that `to:me` matches
"messages where the operator is the explicit recipient: DMs to them,
plus `<@SELFID>` mentions".

The second half is not true. Slack scopes `to:` to a message's
recipient, and a channel message has no recipient, so tagging someone in
a channel does not make the message "to" them. `to:me` returns DMs.
Scoped to a channel the operator was demonstrably tagged in, it returns
nothing at all.

The failure was silent and self-concealing. The sweep still returned a
plausible list of DMs, so it never looked broken — it looked like nobody
had mentioned the operator in a channel. The more DM traffic an account
carried, the more convincing that empty answer was, and an account whose
channel mentions outnumbered its DMs got the emptiest result of all. A
direct question asked in a channel could sit unanswered indefinitely
while the tool reported no pending mentions.

Slack's index does match a channel tag — by the operator's `@handle`,
not by `to:`. Neither query subsumes the other: `to:me` finds DMs that
never spell the handle, and the handle query finds the channel tags
`to:me` cannot see.

## Decision

`MentionQueries(handle, afterDate)` is the single place that knows this.
It returns both queries; every caller runs all of them and merges the
hits, deduped by channel and timestamp.

`auth.test` already returns the handle alongside the user id, so
`SelfHandle` caches it from the response `Self` was making anyway — no
extra call. An empty handle yields the `to:me` query alone, degrading to
the previous DM-only behaviour rather than to nothing.

The handle query also matches the operator's own messages, because their
handle rides along on everything they post. Those are dropped by author
id in both paths.

`limit` now bounds the merged set rather than each query, since the union
is what the caller asked to be bounded.

## Consequences

- A mention in a channel reaches the mentions sweep. This is a
  behaviour change: sweeps that reported nothing may now report work.
- Two search calls per mention pass instead of one.
- `limit` counts merged hits. Ordering stays newest-first, so the cap
  drops the oldest.
- The wrong premise is corrected where it was written down, so the next
  caller does not rebuild the same query from the same bad comment.

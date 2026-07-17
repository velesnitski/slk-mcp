# ADR 052: with_context for search_messages

Date: 2026-07-17
Status: accepted

## Context

Not a bug — `search_messages` worked as designed. But a search hit is a
single, ISOLATED message with no surrounding conversation. A
`from:@user` search in particular returns only that user's lines, never
the messages they were replying to. Reading one such hit out of context
is an easy way to misinterpret a DM (observed live: a `from:@user` hit
was analysed on its own and misread, when the answer was in the two
turns around it that the search never showed).

`get_mentions` already solved this with `with_context` — it inlines a
few messages on each side of a hit. `search_messages` didn't have it.

## Decision

Add `with_context` (bool) + `context_messages` (int, default 3) to
`search_messages`, reusing the exact `get_mentions` machinery:
`Hub.fetchMentionContext` (n messages before/after the hit via
`conversations.history`), `writeContextLines`, and `collectUserIDs` for
name resolution. Context lines render under each hit with the same
`↳` (before) / `↪` (after) prefixes and cross-hit dedup (`shown` set)
as the mentions view. Off by default — same cost profile as
`get_mentions` (one or two `history` calls per hit only when enabled).

## Consequences

- A search hit can now be read in context, closing the isolated-message
  misread failure mode — `search_messages(query, with_context=true)`
  shows the surrounding turns.
- Reuses tested helpers (the context fetch + render are already
  exercised by the `get_mentions` tests); the addition is thin wiring in
  `handleSearchMessages`, so no new bespoke test — the suite stays at
  564. Additive: `with_context=false` (default) is byte-identical to the
  old output.
- Minor release (1.11.0). No new tool; `search_messages` gains two
  optional args.

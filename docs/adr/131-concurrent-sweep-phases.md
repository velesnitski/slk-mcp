# ADR 131: Sweep phases run concurrently; one joined-channel listing per sweep

Date: 2026-11-01
Status: accepted

## Context

Workspaces are swept at the same time (ADR 122) and the unread pass reads
channels with four workers, but inside one workspace the phases of
`get_unread_summary` ran one after another: unread, DM window, priority
channels, thread mentions, own threads, canvases. A sweep took the sum of
its phases. Within the phases, three more things were serial:

- the thread-mention backstop ran its two mention searches and the
  operator's own-posts search back to back;
- the own-thread backstop fetched each thread's replies in turn;
- `users.conversations` was paged three times per sweep — by the unread
  pass, the DM window and the priority channels.

Slack's rate limits are per method and per workspace. The phases call
different methods, so they do not compete for the same budget.

## Decision

1. The phases start together. Only the DM window depends on another
   phase — it reuses the unread pass's history pages (ADR 125) — so the two
   run as one chain beside the others. Results are merged after all phases
   finish, in the same order as before, so the output does not depend on
   which phase finished first. `auth.test` (cached) is resolved first,
   because the canvas phase needs the operator's id.
2. The sweep recorder is safe for concurrent phases, and a deadline note
   names cut phases in sweep order rather than finishing order.
3. The thread-mention searches and the own-posts search run concurrently;
   hits are merged in query order.
4. Own-thread replies are fetched by four workers — the same Tier 3 method
   and politeness budget as the unread pass — and merged in key order.
5. `users.conversations` is shared through a 60-second cache with in-flight
   deduplication: concurrent phases wait on the listing being fetched
   instead of paging it again. The TTL spans one sweep, so a DM someone
   new opens is never hidden for longer than a minute. Errors are not
   cached, and each caller gets its own slice.
6. `show_timings=true` appends the sweep's wall time and each phase's
   duration, because MCP hosts usually drop the server's stderr where the
   same timings are logged.

Worker counts per method are not raised: `conversations.history` and
`conversations.replies` are Tier 3, and more parallel calls on one method
would only trade waiting for 429 retries.

## Consequences

- A sweep takes about as long as its slowest phase chain. In the test
  fixture with three slow search/file phases, about 1 s became about 0.2 s.
- A sweep makes two fewer `users.conversations` listings.

## Experiment: client.counts

`get_unread_summary` checks every joined conversation with
`conversations.info` and, when it has a read marker, a history page — two
calls per conversation, even for one with nothing new. `client.counts`, the
endpoint Slack's clients use for sidebar badges, returns every
conversation's unread state in one call. It is undocumented, may refuse a
user token and can change, so it is only an option:
`unread_source=counts` reads just the conversations it reports as unread
(or with mentions) plus any it does not list, falls back to the full check
on any error, and always prints what it did. A conversation it skips is not
read at all, so new replies in its already-read threads are left to the
thread-mention and own-thread backstops. Whether it becomes the default
depends on the benchmark against real workspaces.

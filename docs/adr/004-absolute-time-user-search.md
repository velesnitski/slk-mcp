# ADR 004 — Absolute-time `since`/`until` on `get_user_messages`

**Status:** accepted
**Date:** 2026-05-12
**Tag at acceptance:** v0.4.2

## Context

The MCP exposes two ways to ask "what has user X been posting":

1. `get_unread_summary` — returns messages newer than the caller's
   `last_read` mark on each channel.
2. `get_user_messages` — workspace search keyed on `from:@user`,
   optionally narrowed by `in:#channel`.

For deadline-driven monitoring ("did user X post the daily status
report in `#status-channel` by 18:00?"), an LLM consumer of this
MCP reached for (1) and inferred "no post today" whenever the
channel was absent from the unread feed. The post had actually
landed — the caller had simply already read the channel, so the
unread feed correctly omitted it.

The synthesis bug was on the consumer side, but the tool surface
made it easy to make: there was no obvious, deterministic primitive
for "messages from X in Y between T1 and T2."

### Options considered

- **a.** Document the failure mode in `get_unread_summary`'s tool
  description ("returns messages newer than last_read; for
  deadline checks, use absolute-time tools"). Cheapest, but relies
  on the consumer reading docs every time.
- **b.** Add a purpose-built `get_last_post_by(channel, user)`
  tool returning `{found, ts, body}`. Very tight contract; new
  surface area overlaps existing tools.
- **c.** Add `since`/`until` to the existing `get_user_messages`,
  mapping to Slack's `after:`/`before:` search operators. Smallest
  diff, broadest applicability (also useful for "what did Bob
  ship last sprint?" style queries), no new tool to learn.

## Decision

Use **(c)**. `get_user_messages` gains optional `since` and `until`
parameters, each validated as `YYYY-MM-DD` and passed through to
Slack search verbatim. The tool description explicitly recommends
this tool over `get_unread_summary` for deadline-style questions,
documenting the read-state pitfall once in the place the consumer
is most likely to look.

The query builder is factored into a standalone
`buildUserMessagesQuery(user, channel, since, until)` helper with
unit-test coverage, so future refactors of the surrounding handler
can't accidentally drop a clause.

## Consequences

- One additional call shape covers the deadline-monitoring use case
  without spinning up a new tool name.
- Slack's own date-operator semantics leak through: `after:`/`before:`
  inclusivity follows whatever Slack defines, not a re-mapped
  convention. The tool description names the operators explicitly so
  the consumer knows what they're calling.
- `get_unread_summary` is unchanged. It's still the right tool for
  "what arrived since I last looked" — just not for "did this thing
  happen at all." The boundary is now documented.
- Date validation happens in the handler; a malformed `since`/`until`
  returns an error to the caller rather than being silently dropped
  into the search query.

## Validation

- `TestBuildUserMessagesQuery_*` in
  `internal/tools/user_messages_test.go` covers:
  - user-only query;
  - leading `#` stripped from the channel argument;
  - both `since` and `until` present;
  - either bound present alone.
- Date-format errors are surfaced to the caller as
  `"since must be YYYY-MM-DD"` / `"until must be YYYY-MM-DD"` (caught
  in the handler via `time.Parse`).

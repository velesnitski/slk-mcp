# ADR 116: dates on old messages, external DM peers, system stamps, rich text

Date: 2026-10-11
Status: accepted

## Context

A week-long catch-up sweep exposed four reading defects:

- Every digest line carried a bare clock time. Over a multi-day window a
  message from last week and one from this morning looked identical, so
  a stale item read as fresh.
- A DM partner from another organisation (Slack Connect) is not in
  `users.list`, so `@handle` never resolved for them even though the
  operator has a DM with that person.
- Conversation rename stamps ("named the conversation …", "has renamed
  the channel from … to …") surfaced as pending mentions, as the join
  notice did before ADR 115.
- `rich_text` blocks — the body of Slackbot reminders, workflow and most
  app posts — were not read, so those messages rendered as
  `[blocks: 1]`.

## Decision

- `format.ClockLabel`: `15:04` for today, `02.01 15:04` for any other
  day. Applied to message lines, thread headers and thread context lines.
  Today's sweep is unchanged.
- `IDForHandle` falls back to DM peers missing from the roster: one
  `conversations.list` page of IMs, then `users.info` per unknown peer,
  capped at 50 lookups. A roster match never takes this path.
- The system-notice pattern used by `drop_closing_acks` also covers the
  two rename stamps, anchored to the whole body so a request that merely
  mentions renaming still surfaces.
- `blocksText` reads `rich_text` sections, lists, quotes and
  preformatted blocks; mentions are emitted as Slack markup so existing
  user and channel resolution renders them. A block with no prose keeps
  the `[blocks: N]` count.

## Consequences

- Multi-day digests can be read without guessing which day a line is
  from; lines from earlier days cost six more characters.
- An unresolvable handle costs one extra `conversations.list` and up to
  50 `users.info` calls before failing.
- App and workflow posts become readable in digests and the unread
  sweep.

# ADR 096: the roster carries Slack IDs

Date: 2026-09-07
Status: accepted

## Context

`list_users` rendered handle, real name, job title, role flags and the
profile-update date. Everything a human needs to recognise a colleague,
and nothing that connects them to what the API actually returns.

Message bodies, search results and API payloads carry IDs. Most of the
time the renderer resolves them and the reader never sees one — but the
resolution can fail, and there are shapes it does not cover at all: a
mention that has lost its markup, a payload the renderer surfaces
verbatim, an ID quoted inside a bot's own text.

When that happened, the roster was no help. It could answer "who is
this handle" but not "who is `U…`", so the only route left was
inference from context — which is guesswork, and guesswork about
which colleague said what is worse than an unresolved ID.

## Decision

Lead each roster row with the Slack ID, then handle, name, title,
flags, dates.

The row rendering moves into `renderUserRows`, a pure function, so the
format is covered by tests rather than by a live roster fetch.

## Consequences

- Any unresolved `U…` on any surface can be traced through one call.
- The ID leads the line because it is the join key; the human-readable
  fields follow in the order a person scans them.
- Rows are one column wider. That is the cost of the lookup working in
  both directions.

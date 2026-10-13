# ADR 119: get_message names who reacted

Date: 2026-10-13
Status: accepted

## Context

`get_message` reported reactions as counts only (`:tada: ×41`). Questions
such as "who acknowledged this announcement" or "who has not reacted"
need the people, and the only way to get them was a raw Web API call
outside the server. Slack already returns user ids per reaction in
`conversations.history`, but may cut a busy reaction's list short of its
count; `reactions.get` with `full=true` is the call that promises all of
them.

## Decision

- New opt-in `reactors` flag on `get_message`. Off by default, so the
  ordinary drill-in stays as short as before.
- With the flag, the history payload is used as is when every list is
  complete — no extra call. When any reaction lists fewer users than its
  count, one `reactions.get` (full) replaces the lists.
- `MessageService.Reactions` retries with the user token on an auth or
  scope refusal, the same rule as file downloads; any other failure is
  not retried.
- If the completion fails, the partial lists are still rendered, each
  short one marked `(+N not listed)`, with a `note: partial` line
  carrying the error. A partial answer is labelled, never passed off as
  complete.
- Output: one line per emoji, then `everyone who reacted (N): …`,
  de-duplicated and sorted. Unresolvable ids are shown raw.

## Consequences

- `reactions:read` is optional and only needed when Slack truncates;
  README lists it.
- The roster makes "did not react" a set difference against
  `get_channel_info include_members=true`; the tool does not compute it
  itself, since the right reference group (channel, team, invitees)
  depends on the question.

# ADR 066: list_scheduled_messages — surface the API-only scope

Date: 2026-07-30
Status: accepted

## Context

`list_scheduled_messages` (ADR 052) rendered an empty result as
"no scheduled messages". Live failure: the operator had **5 messages
scheduled from the Slack client**, and the tool reported none — the
assistant relayed "нет отложенных сообщений", a false negative on a
question about the operator's own outgoing queue.

The implementation is correct (user token, full pagination, no filters).
The gap is Slack's: **`chat.scheduledMessages.list` only returns messages
scheduled through the API by a token.** Messages queued in the Slack UI
are stored as drafts-with-send-time and are not exposed by that method —
the same class of gap as `subscriptions.thread.*` (ADR: rejected spike),
reachable only with a browser `xoxc` token we deliberately do not use.

## Decision

Stop rendering the ambiguous empty case; make the scope explicit at both
the result and the contract level.

- `scheduledEmptyMsg(label)`: "no **API-scheduled** messages … note:
  chat.scheduledMessages.list only returns messages scheduled VIA THE
  API; anything you queued in the Slack UI is invisible to it, so check
  Slack → Drafts & sent to see those."
- Tool description states the limit up front and instructs the caller
  never to report the queue as empty from this tool alone.

No attempt to "fix" the coverage: doing so would require an `xoxc`
token, already validated as a dead end.

## Consequences

- The tool can no longer produce a confident false negative about the
  operator's queue; the caveat travels with every empty result.
- Non-empty output unchanged.
- 594 → 595 tests (label preserved, caveat present, no bare
  "no scheduled messages" prefix). Minor release (1.23.0).

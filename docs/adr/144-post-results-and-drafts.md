# ADR 144: post a result to Slack, and draft the follow-ups

Date: 2026-11-11
Status: accepted

## Context

The morning question – what do I owe, who owes me – is answered by
`get_my_actions`, but only when someone asks. And an item on that list
usually calls for one short message: "not forgotten", "any news?",
"seen, answer today". Writing each by hand, in the right DM or thread,
is the friction that keeps items open.

## Decision

- **`--post` in headless mode.** `slk-mcp run <tool> … --post @me` also
  sends the result through the server's own `post_message`, so
  `SLACK_READ_ONLY` and `DISABLED_TOOLS` apply as they do to a client.
  Mentions are not resolved: a digest names people and must not ping
  them. An all-clear result is not posted unless `--post-empty`; a
  result longer than a Slack message is cut at a line break and says so.
  Scheduling (launchd, cron) stays outside the server.
- **`post_message` targets.** `@me` is the operator's own DM; `@handle`
  and a user ID open the DM with that person, as the read tools already
  resolve them.
- **`get_my_actions drafts=true`.** Under each listed item there is a
  draft and the `post_message` arguments that would send it: a reminder
  for a promise, a nudge for a request of the operator's, an
  acknowledgement for an ask. It goes to the item's DM, or its thread in
  a channel; a promise said into a channel with no thread and no one
  named gets none. Meeting promises get one when their addressee was
  found in Slack. The draft follows the item's language. Nothing is
  sent: a person approves every message.

## Consequences

- Drafts are templates – a starting point to edit, not prose to send
  blind.
- A scheduled digest needs the server's environment in the job; keeping
  it in an owner-only file is the operator's part.

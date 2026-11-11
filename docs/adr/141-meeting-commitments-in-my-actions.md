# ADR 141: meeting commitments in get_my_actions

Date: 2026-11-11
Status: accepted

## Context

Many commitments are made on calls, not in Slack. A meeting-notes tool
can list them, but it cannot tell which are done: completion shows up in
Slack, as a message to the person the promise was made to. Listing both
sources side by side also repeats every promise that was said on a call
and then written down in Slack.

## Decision

`get_my_actions` reads meeting commitments from a command and judges
them against Slack.

- **The command is the contract.** `SLACK_MEETING_ITEMS_CMD` is run
  without a shell, with `since=<days>d` appended, and its stdout is
  parsed as JSON: the headless envelope `{"ok":…,"items":[…]}` or a bare
  `{"items":[…]}`. wispr-mcp's
  `run list_action_items owner=me format=json --json` fits. slk-mcp
  links nothing from the notes tool and the notes tool never sees
  Slack. Unknown fields are ignored. `include_meetings=false` turns the
  merge off for one call.
- **Only the operator's own actions** (`mine` true, `kind` action).
  Unresolved owners and decisions are never counted as the operator's.
- **Restated in Slack → counted, not repeated.** A Slack commitment from
  a day before the call to two days after, sharing two content words,
  and naming the same person when the meeting named one, is the same
  commitment; the Slack line is richer, so the meeting one is counted.
- **Delivered → "probably done", with the message.** The DM with the
  person the promise was made to is searched from the meeting on. A
  later message of the operator's that shares two content words, or
  carries a link or attachment when the promise was to hand something
  over, is shown as evidence. It is never silently closed: the reader
  checks it.
- **Words, not a model.** Content words are cut to five letters so
  Russian inflections meet; names and filler words are ignored.
- **People by name, never guessed.** A participant name maps to a Slack
  user only when exactly one person has it.
- **Failure is a note.** A missing, slow or failing command adds one
  line; the Slack part is unaffected.

## Consequences

- One DM search per meeting commitment with a known person, run in
  parallel within the sweep timeout.
- A delivery written in different words, or sent in a channel rather
  than the DM, is not found; the item stays open.
- Deadlines come from the notes tool; the phrase table shared with it
  (ADR 140) keeps both sides on the same date.

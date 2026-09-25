# ADR 112: read is not the same as seen in the digest

Date: 2026-09-25
Status: accepted

## Context

A channel where roadmap decisions and priority syncs were announced
never appeared in `get_unread_summary`, although the operator named it
one of the two most important channels in that workspace. It looked
like a bug. It was the design working as specified.

The sweep is built on `last_read`: it shows what lies after the
operator's read marker. Slack's API confirmed the operator had opened
that channel in the client fifteen minutes after the last message — so
by the time the sweep ran, there was nothing unread in it. A second
channel the operator had not opened for a week appeared every time.

The effect is perverse for exactly the channels that matter most. A
channel the operator follows closely is one they read quickly, often on
a phone and in passing — and a decision skimmed that way then appears in
no digest at all. The sweep ends up surfacing the channels the operator
neglects and hiding the ones they care about.

`dm_window_hours` already solved the same problem for direct messages:
show a time window regardless of `last_read`. Channels had no
equivalent.

While extending that path, it turned out to carry the defect ADR 111
fixed elsewhere: the DM window fetched `conversations.history` with
`oldest` set, which returns the page adjacent to `oldest`. A DM with
more messages in the window than the per-channel cap showed its oldest
ones and dropped the newest. A test pinned the wrong behaviour by
asserting the `oldest` parameter.

## Decision

Each workspace can declare priority channels — `SLACK_PRIORITY_CHANNELS`
for the primary, `priority_channels` in each `SLACK_WORKSPACES` entry,
names or IDs. The lists stay scoped to their workspace through
`WorkspaceViews`, like `channels`.

`get_unread_summary` fetches every priority channel for its window
whether or not it has been read, and merges it with the unread sweep:

- window: `priority_hours` (default 24, `0` turns it off); with an
  `after` cursor, everything newer than the cursor, so a delta pull
  shows only what is new there;
- priority channels sort first, ahead of the urgency ranking, because
  under `max_chars` the tail is what gets dropped;
- the heading is marked `★`, plus `· read` when every message shown is at
  or before the operator's `last_read` — the case the feature exists for;
- they are never collapsed by the low-signal renderer;
- a listed channel that cannot be found or is not joined is named in the
  output. A typo or a lost membership must not make a priority channel
  silently stop appearing.

The recent-activity fetch shared by the DM window and priority channels
is anchored at now and trimmed to the window locally (ADR 111).

## Consequences

- The channels the operator names as important are always in the
  digest, including after they have been read.
- One extra `conversations.info` and one `conversations.history` call
  per priority channel per sweep. Lists are expected to be short.
- `dm_window_hours` now shows the newest messages of a busy DM instead
  of the oldest ones in its window.
- Channel names in the priority list live in the operator's
  environment, never in this repository.

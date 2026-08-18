# ADR 078: bot feeds rank below human channels

Date: 2026-08-18
Status: accepted

## Context

A sweep of 56 channels emitted the DMs, then a wall of bot feeds, then
dropped 38 channels on the `max_chars` cap — among them every low-volume
human channel in the workspace.

`RankUnread` starts from `rank := len(cu.Messages)` and adds
`UrgencyScore`, which rewards `error` / `failed` / `alert`. Volume and an
error-shaped vocabulary are precisely what a machine-driven channel is
made of, so the two loudest inputs to the rank were measuring the wrong
thing. A feed of fifty identical `[attached: 1]` lines scored 50 before
urgency; a channel with three human messages scored 3.

The renderer already knew better — `DetectLogChannel` / `DetectGitChannel`
/ `DetectLowSignalChannel` collapse these to a histogram or a one-liner.
The detection simply never reached the ranker, so a channel could be
rendered as a feed while being ordered as a conversation.

## Decision

Add `feedPenalty` as a tier, symmetric to the `dmBonus` tier that already
exists above ordinary channels (ADR 020). `IsBotFeed` groups the three
existing detectors so the ranker's notion of a feed is identical to the
renderer's, by construction.

The gap is wide enough that no volume climbs back out — which is the same
argument the tier constants were chosen on originally.

It is a demotion, not a mute: `mentionBonus` still stacks, so a ping
inside a CI channel surfaces above ordinary channels. It just sits below
a ping from a person.

Also: the `max_chars` footer now carries each omitted channel's unread
count. A bare list of names cannot tell you whether a drill-in is worth
a call.

## Consequences

- Under a cap, human discussion is emitted before bot output. What gets
  truncated is the noisy tail, which is what a cap should truncate.
- Two existing tests failed and both were fixture accidents, not
  regressions: they built "busy" channels out of 1- and 13-character
  bodies, which is the literal definition `DetectLowSignalChannel`
  matches (>=5 messages, average body under 16 chars). Bodies were made
  realistic; the assertions are unchanged.
- `skip_log_mode` remains for dropping feeds entirely; this only reorders
  them.
- Raising `max_chars` was the obvious response and would have been the
  wrong one: it buys more output while leaving the ordering — and so the
  choice of what survives a cap — inverted.

# ADR 092: bot payloads render their text, not their count

Date: 2026-09-07
Status: accepted

## Context

Whole classes of channel are written entirely by bots: alert feeds,
scanner reports, billing notices, CI summaries. They post with an empty
`text` field and put everything a human reads into a legacy attachment
or a Block Kit block.

The renderer surfaced those as `[attached: 1]`. The marker existed for a
good reason — a message with no body and no files would otherwise render
as an empty line, and the reader would never learn there was anything to
follow. But a count is not a summary. Reading one of those channels
through this server produced a column of identical `[attached: 1]`
lines, and the only way to learn what any of them said was to open
Slack — which is the one thing this server exists to avoid.

The payloads were never opaque. Sampling the API showed every such
message carrying a `fallback` string of 20–70 characters, set by the
bot precisely so a client that cannot draw the rich form still shows
something true. Many also carried `title`, `title_link`, `fields`, or a
nested block set. All of it was parsed into the typed struct and then
discarded at render time.

## Decision

Lift the text out. `renderHiddenPayloadMarker` now walks each
attachment for `pretext`, `title`, `text`, its fields, and any nested
blocks, then the message's own block set, and joins what it finds.

Four properties keep it honest:

- **`fallback` is a last resort, not an addition.** Slack sets it to a
  copy of the title on most bot posts; emitting both would double every
  alert line. It is consulted only when the structured fields yield
  nothing.
- **Identical strings collapse.** The same sentence often appears as
  title, text and fallback at once.
- **The output is capped** at `HiddenPayloadLimit` with an exact
  overflow count, like every other truncation here. One verbose bot
  cannot take over a digest.
- **The counter survives** for payloads that genuinely carry no prose.
  The line must never be silently empty.

Only section, header and context blocks are walked. Layout blocks
carry no prose, and unknown block types are skipped rather than
guessed at.

## Consequences

- Bot-driven channels are readable without opening Slack.
- Huddle detection still runs first: a huddle is an event, not text.
- Messages that have a body are untouched — this path only fires when
  body and file list are both empty, so URL-preview messages stay clean.

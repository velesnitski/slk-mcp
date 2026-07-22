# ADR 058: read_canvas — read a channel's canvas document

Date: 2026-07-22
Status: accepted

## Context

Teams increasingly park the durable stuff — runbooks, on-call rotations,
incident-process drafts — in a channel **Canvas** rather than in
messages. A Slack canvas is not a message: it's a file-backed document
hung off the channel, so `get_channel_digest` (and every history-based
tool) is structurally blind to it. Asked "can we read the channel
canvas?", the honest answer was "not yet" — but every primitive needed
already existed in the codebase.

## Decision

Add a `read_canvas` tool. The resolution path is three hops, each on
machinery we already ship:

1. `resolveConversation` (ADR 054) turns the `channel` arg — `#name`,
   `@handle` DM, bare `U…`, or canonical id — into a conversation id.
2. `conversations.info` exposes `properties.canvas` (`{file_id,
   is_empty}`) — slack-go surfaces it as `Channel.Properties.Canvas`.
   Empty / absent canvas → a clear message, not an error dump.
3. The canvas body is fetched like any other Slack file:
   `files.info(file_id)` → download `url_private` via the audio
   pipeline's authenticated `DownloadFile` primitive.

`canvasToText` (pure, unit-tested) renders the bytes: Slack canvases
download as HTML (older) or markdown (newer), so it strips HTML to text
(dropping `<script>/<style>`, turning list items into bullets and block
tags into newlines, unescaping entities) and passes markdown/plain
through untouched. A 200 KB byte cap + `_(canvas truncated)_` marker
keeps a runaway doc from blowing the MCP result.

## Consequences

- "прочитай канвас в #channel" is one call; the durable channel doc is
  finally reachable from the assistant.
- No new client contracts — reuses `Channels().Info`,
  `Messages().FileInfo`, `Messages().DownloadFile`. Needs `files:read`
  (already required by audio/image) plus the channel read scope the
  digest already uses.
- Read-only; format-robust by construction (HTML or markdown). Editing
  canvases is deliberately out of scope.
- 577 → 581 tests (HTML reduction, markdown passthrough, byte cap, blank
  collapse). Minor release (1.15.0).

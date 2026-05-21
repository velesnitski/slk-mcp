# ADR 008 — Permalink-ID short-circuit + hidden-payload markers

**Status:** accepted
**Date:** 2026-05-21
**Tag at acceptance:** v0.4.6

## Context

Two bugs surfaced in the same diagnostic session:

### Bug 1 — `get_thread(permalink=...)` returned "channel not found"

The handler in `internal/tools/threads.go` does:

```go
p, _ := slack.ParseSlackPermalink(permalink)
if channel == "" { channel = p.ChannelID }
…
channelID, err := h.Channels().ResolveID(ctx, channel)
```

`p.ChannelID` is a Slack canonical channel ID (e.g. `C0ABC1234DE`).
`ResolveID` then treats it as a channel *name*, scans the workspace
listing, doesn't find anything matching, and returns an error like
`channel #C0ABC1234DE not found`. The same shape exists in
`mark_read`.

There was already an `IsChannelID` helper (v0.3.26) that distinguishes
canonical IDs from names — but no code path used it on the input.

### Bug 2 — `MessageLine` rendered an effectively empty line

When fetching a thread parent that carried content only in
`Attachments` (legacy rich attachment API, forwarded messages) or
`Blocks` (Block Kit structured content), `MessageLine` produced:

```
[12:03 alice]   :eyes:(1) (1 replies)
```

Two spaces between the bracket and the next field — `Text` was
empty and the renderer had nothing else to write between body and
reactions. A reader couldn't tell whether the user posted an empty
message or whether the renderer dropped a payload it didn't know
how to surface.

The existing `renderFiles` already handles `msg.Files`. The gap was
`msg.Attachments` and `msg.Blocks.BlockSet`, both of which the
slack-go SDK populates for these message shapes.

### Options considered

For Bug 1:
- **a.** Fix at every handler — repeat the `IsChannelID` short-circuit
  in `get_thread`, `mark_read`, and every future consumer.
- **b.** Fix at the service — `ResolveID` itself short-circuits when
  the input is already a canonical ID.

For Bug 2:
- **c.** Add full Attachments / Blocks rendering. Heavy: Attachments
  carry rich nested content (fields, action buttons, color bars);
  Blocks are even more nested. Big surface for diminishing return —
  the LLM consumer can always drill into the permalink for full
  fidelity.
- **d.** Append a short marker (`[attached: N]`, `[blocks: N]`)
  only when the message would otherwise render blank — body text
  AND files both empty. URL-preview messages (text + 1 attachment)
  stay clean.

## Decision

- **Bug 1:** Use **(b)**. The short-circuit lives in
  `ChannelService.ResolveID` so every caller benefits in one place.
  The contract change is purely additive — names continue to work,
  IDs now also work, no caller breaks.
- **Bug 2:** Use **(d)**. New helper
  `format.renderHiddenPayloadMarker(msg)` returns a short marker
  string. `MessageLine` calls it only when `body == ""` and `Files`
  is empty — the existing happy path is unaffected.

`HasContent` is updated in the same diff to also count Attachments
and Blocks; otherwise an `Attachments`-only message would be filtered
out by `writeContextLines` and similar helpers, defeating the new
marker.

## Consequences

- **Permalink callers work end-to-end.** Both `get_thread(permalink)`
  and `mark_read(permalink)` resolve correctly without changes to
  their handlers. Tests in `internal/slack/channels_test.go` pin the
  short-circuit behaviour (with and without leading `#`).
- **No more silent empty lines.** A message rendered through
  `MessageLine` will always carry *something* between the author and
  reactions — body text, file marker, or a hidden-payload marker.
  The reader gets a deterministic signal that the message is real
  and the permalink will have more.
- **URL-preview noise prevented.** Because the marker is gated on
  `body == ""`, a normal text message with an Attachment-as-preview
  doesn't pick up the marker. We accept the trade-off that a *very*
  short-text message (`"see this"`) plus an Attachment will not be
  flagged — the reader has enough body to know there's something
  there.
- **No new tool surface.** Both fixes are layer-internal; no MCP
  contract changes.
- **`HasContent` semantics broadened.** A message with only
  Attachments or only Blocks now counts as having content. This is
  intentional — it matches the spirit of the original check
  (anything worth showing) and was the bug that masked Bug 2 in
  practice.

## Validation

- `TestResolveID_shortCircuitsOnCanonicalID` and `…WithLeadingHash`
  pin the short-circuit. Both call `ResolveID` with a nil-API
  `ChannelService` — if the short-circuit ever regresses, the test
  panics on the nil dereference instead of silently passing.
- `TestRenderHiddenPayloadMarker_*` covers empty/attachments-only/
  blocks-only/both cases.
- `TestMessageLine_flagsHiddenPayloadWhenBodyEmpty` is the
  end-to-end test for the renderer integration.
- `TestMessageLine_doesNotFlagWhenBodyHasText` pins the
  URL-preview-noise-prevention invariant.
- `TestHasContent_attachmentsOnly` / `…blocksOnly` cover the
  broadened filter.
- Full suite: 341 → 351 (+10) green.

## Out of scope

- Rendering Attachment fields, fallback text, or Block content as
  plain text. The marker count is the 80/20; full structured
  rendering would double the surface area of `format/` for content
  the LLM can drill into via `get_thread` or the permalink.
- Distinguishing forwarded messages (which use Attachments) from
  integration posts (also Attachments). Both produce the same
  marker; that's good enough for the consumer's signal.

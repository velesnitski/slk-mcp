# ADR 054: @handle / user-id conversation refs in digest and thread tools

Date: 2026-07-20
Status: accepted

## Context

Reading a DM by person required a ritual: `search from:@X` → extract the
`D…` id from a hit's permalink → `get_channel_digest(channel="D…")`.
Observed ~6 times in one working day ("what did X write me?", "validate
my reply to X"). Meanwhile the audio/image tools already resolved
`@handle → DM` (ADR 046) — the capability existed but only on one
surface. Worse, `get_unread_summary` itself prints DM headers as
`#U0AAAA1111B`, a shape no read tool accepted back.

## Decision

Promote conversation-ref resolution to a shared, classified resolver and
use it in `get_channel_digest` and `get_thread`.

- New `internal/tools/conversation.go`: pure
  `classifyConversationRef(ref) → (kind, token)` deciding between
  `@handle` (roster lookup + `conversations.open`), bare `U…`/`W…` user
  id (straight to `conversations.open` — accepts the digest's own
  `#U…` copy-paste shape), and everything else (existing `ResolveID`,
  which already short-circuits `C/G/D` ids). A leading `#` is tolerated
  on every form because digests prefix everything with `#`.
- `resolveConversation` / `resolveAuthor` move out of audio.go and lose
  the awkward two-hub signature (now methods on the scoped Hub).
- New `slack.IsUserID` (`U…` classic + `W…` enterprise-grid), mirroring
  `IsChannelID`/`IsConversationID`.
- `channelDigestRange` (get_channel_digest) and the `get_thread` handler
  resolve via `resolveConversation` instead of bare `ResolveID`.

## Consequences

- "digest the DM with @teammate" and "digest #U0AAAA1111B" are single
  calls — the search-for-the-D-id round-trip is gone. get_thread accepts
  the same forms for its `channel` arg.
- `@handle`/user-id DMs need `users:read` + `im:write` (same as
  latest-mode, ADR 046); plain channel names behave exactly as before —
  additive, no callers change.
- The classifier is pure and unit-tested (handle, `#@handle`, `U…`,
  `#U…`, `W…`, lowercase-name false-positives, canonical ids untouched).
  Audio/image latest-mode now shares the identical resolution path.

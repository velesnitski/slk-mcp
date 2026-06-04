# ADR 021 — three digest-usability fixes from a week of dogfooding

**Status:** accepted
**Date:** 2026-06-04
**Tag at acceptance:** v0.4.19

## Context

A week of using `get_unread_summary` / `get_mentions` / `get_thread`
for daily digests surfaced three rough edges. None are correctness
bugs in the silent-miss sense (no data is lost), but each adds
friction or noise to every digest. Bundled here because they share a
theme — "the tools work, but the output and the drill-in paths have
papercuts."

### 1. DM drill-in is broken (the only real bug)

`IsChannelID` deliberately excluded `D…` (DM) IDs:

```go
// `D…` DMs are intentionally excluded —
// callers asking for a "channel name" never mean a DM
```

That reasoning is correct for *name* resolution but wrong for the
*permalink* path. `get_thread(permalink)` parses a DM permalink into a
`D…` channel ID, then routes it through `ResolveID`, which — because
`IsChannelID` rejects `D…` — treats it as a *name*, fails the
workspace listing, and returns "channel #D0… not found". Same for
`get_channel_digest(channel="D…")`.

Hit live: drilling into a CMO DM to recover the thread context
required falling back to `search_messages`, because neither
`get_thread` nor `get_channel_digest` could address the DM by ID.

### 2. Automation senders pollute `get_mentions`

Every single day, `get_mentions(pending_only=true)` surfaced a
calendar bot's "@you Today is …" ping and Slackbot's invite-request
notices as "pending mentions". These can't be replied to and are
never actionable — pure noise, and a recurring false positive under
`pending_only`.

### 3. Empty "(no activity)" stub blocks

A channel with no top-level messages but lingering thread replies
(typical for a DM pulled in by `dm_window_hours`) rendered as

```
## #D0…
(no activity)
```

— an empty stub that wastes a line in the aggregate digest. For a
single-channel `get_channel_digest` that "(no activity)" answer is
useful ("you asked, there's nothing"), but in the multi-channel
unread sweep it's just noise.

## Decision

### 1. `IsConversationID` + broaden the `ResolveID` short-circuit

Add `IsConversationID` (accepts `C`/`G`/`D`); `ResolveID` now
short-circuits on it instead of the narrower `IsChannelID`. Both
delegate to a shared `isCanonicalID(s, prefixes)`. `IsChannelID`
keeps its `C`/`G` semantics for any "is this a channel?" caller.

Safe because no real channel *name* matches `D[A-Z0-9]{7,}` (names
are lowercase + hyphens), so admitting `D…` to the short-circuit
cannot mis-resolve a legitimate name as an ID.

### 2. `filterBotSenders` on every `get_mentions` sweep

A small allowlist of automation identities (`google_calendar`,
`google_drive`/`googledrive`, `slackbot`, `USLACKBOT`) is dropped
from mentions output unconditionally — matched case-insensitively on
the hit's `Username`, plus the `USLACKBOT` user-id sentinel. Applied
to all `get_mentions` calls, not just `pending_only`, since these
senders are never a real mention regardless of mode.

### 3. `WithOmitEmpty` digest option

New `DigestOption` makes `ChannelDigest` return `""` instead of the
`(no activity)` line when a channel has no displayable top-level
messages. The unread sweep passes it (its existing
`if rendered == "" { continue }` then drops the channel);
single-channel `get_channel_digest` does not, preserving the
informative `(no activity)` answer.

## Consequences

- **DMs are now first-class drill-in targets.** `get_thread` and
  `get_channel_digest` work on `D…` IDs and DM permalinks. The
  `search_messages` workaround is no longer needed.
- **`get_mentions` is quieter by default.** No behaviour flag — the
  bot filter is always on. If a workflow ever genuinely needs a
  calendar/Slackbot hit, that's a future opt-out, not the default.
- **Aggregate digests drop empty stubs** without changing
  single-channel semantics.
- **No public API/signature changes.** `IsChannelID` unchanged;
  `IsConversationID` and `WithOmitEmpty` are additive.

## Validation

- `go vet ./...` — clean.
- `go test -race -count=1 ./...` — green.
- New tests: `IsConversationID` table (incl. the DM case
  `IsChannelID` rejects), `ResolveID` DM-ID short-circuit,
  `filterBotSenders` (calendar/drive/slackbot dropped, humans kept),
  `WithOmitEmpty` (suppresses `(no activity)`, keeps real content).

## Out of scope

- A `since`/delta digest mode (return only what changed since the
  last call). This is the highest-value *ergonomic* change for a
  heavy-polling workflow, but it's a design decision with state /
  cursor questions of its own — separate ADR when taken up.
- Making the bot-sender list configurable. A hardcoded allowlist of
  the few platform automations is sufficient until a real custom
  integration needs to surface as a mention.

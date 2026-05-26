# ADR 014 — DM-window silent-miss bug

**Status:** accepted
**Date:** 2026-05-26
**Tag at acceptance:** v0.4.12

## Context

Production observation: a message the operator just sent to a DM
(no incoming reply yet, message confirmed visible in Slack UI)
didn't surface in `get_unread_summary(dm_window_hours=12)` even
though it was well within the time window. The truncated
unread-only view of that DM (showing only old incoming messages)
persisted instead of being refreshed by the DM-window override.

Forensics: the DM channel had `last_read` BEFORE the older
incoming messages, so `UnreadAll` returned the channel with a few
stale unread items. `RecentDMActivity` *should* have returned the
same channel with full last-12h history (including the recent
outgoing send), and `mergeDMOverride` *should* have replaced base
with override. Neither happened.

Two cooperating bugs:

### Bug 1 — `mergeDMOverride` conditional

```go
if replacement, ok := byID[b.Channel.ID]; ok && (b.Channel.IsIM || b.Channel.IsMpIM) {
    out = append(out, replacement)
} else {
    out = append(out, b)
}
```

The `IsIM/IsMpIM` conditional on the **base** entry was intended
as a defensive guard against weird cross-type collisions. In
practice, Slack's `users.conversations` doesn't always populate
those booleans for DMs whose state is read-stale on the listing
side. Result: a legitimate DM had `IsIM=false` on the base side,
the conditional rejected the replacement, and the override (which
DID have the fresh content) was silently dropped.

### Bug 2 — `RecentDMActivity` worker filter

Same brittle check on the worker side:

```go
if !ch.IsIM && !ch.IsMpIM {
    continue
}
```

If `users.conversations` returned the DM without the flags, the
worker skipped it before `dmHistorySince` could even run — so
override didn't include the channel at all, and bug 1 (merge
conditional) was a fall-back miss.

### Options considered

- **a.** Always re-fetch `conversations.info` for every channel
  returned by `users.conversations` to authoritatively read
  IsIM/IsMpIM. Heavy — N extra API calls per sweep just to verify
  flags that should already be present.
- **b.** Trust the channel-ID prefix when the boolean is missing.
  Slack guarantees `D…` for IM and `G…` + `mpdm-` name for MPIM.
  No extra API calls, no protocol change.
- **c.** Drop the merge conditional and rely on the override side
  to have already filtered correctly. Override is built ONLY from
  channels that passed `isDirectMessage`, so any match in the
  override map is by definition a DM.

## Decision

Use **(b) + (c)**. Concretely:

1. New `slack.isDirectMessage(ch)` helper: returns `true` when
   `IsIM`, `IsMpIM`, ID starts with `D`, or (ID starts with `G`
   AND name starts with `mpdm-`). Private group channels with
   non-`mpdm-` names stay correctly classified as non-DMs.
2. `RecentDMActivity` worker calls `isDirectMessage(ch)` instead
   of the inline boolean check.
3. `mergeDMOverride` drops the `(b.Channel.IsIM || b.Channel.IsMpIM)`
   conditional. If override has the channel ID, replace. The
   override side has already filtered to DMs in step (2).

## Consequences

- **Silent-miss closed.** An outgoing-only DM now surfaces in the
  time window correctly; the operator gets an honest view of
  recently-sent messages even when `last_read` is past them.
- **No extra API calls.** The ID-prefix fallback is a pure local
  decision; nothing additional is fetched from Slack.
- **Behavioural correctness for private groups.** `G…` channels
  that are *not* multi-party DMs (regular private groups, e.g.
  `#secret-project`) continue to be excluded from the DM filter
  via the `mpdm-` name check. The fix doesn't accidentally widen
  the DM net.
- **Trust boundary moves to one side.** Previously the merge
  rechecked DM-ness "just in case." Now there's a single
  authoritative classifier (`isDirectMessage`) on the override
  build side; the merge trusts that contract. Easier to reason
  about, harder to bit-rot.
- **No new tool surface.** Pure internal robustness fix; tools
  and interfaces unchanged.

## Validation

- `TestIsDirectMessage_flagsSetCorrectly` (4 cases) — happy path:
  flags set → classifier trusts them. Public channel and private
  group correctly identified as non-DMs.
- `TestIsDirectMessage_fallsBackOnIDPrefix` (4 cases) — bug-fix
  path: flags missing, ID prefix takes over. `G…` without `mpdm-`
  prefix stays non-DM.
- `TestMergeDMOverride_replacesEvenWhenBaseIsIMNotSet` —
  end-to-end: base entry has `IsIM=false` (the production
  symptom), override has the fresh outgoing reply, merge produces
  the correct full view.
- Full suite: 385 → 395 green (+10).

## Out of scope

- DM detection by user-token introspection (e.g., calling
  `conversations.info` to authoritatively re-read flags). Not
  needed — the ID-prefix fallback is correct by Slack's documented
  ID scheme.
- Channel-naming heuristic changes for MPIMs. Slack's
  `mpdm-<users>-<n>` convention has been stable for >5 years; we
  rely on it the same way ADR 002 relies on the disjoint-prefix
  guarantee for the unified id→name refs map.
- Generalising `isDirectMessage` to other call sites. Other DM-
  sensitive paths (e.g., the renderer's `channelDisplayLabel`)
  already use their own ID-prefix logic; consolidating is a
  separate refactor.

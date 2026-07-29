# ADR 065: conversation-kind-aware default for with_replies

Date: 2026-07-29
Status: accepted

## Context

`with_replies` (ADR 057) defaults to false — right for channels, where
fan-out × threads multiplies API calls. Wrong for DMs: a direct message
carries few messages, and a **thread reply there IS the conversation**.
Hit live minutes after ADR 064 shipped: reading an analyst's DM with
`get_channel_digest` showed nothing of her substantive reply, because it
lived in a thread and replies weren't fetched — the same blind spot ADR
064 had just closed for the unread sweep, still open on the drill-in
path.

## Decision

`with_replies` now defaults **per conversation kind**:

- **DM → true.** Threads are inlined automatically; a DM read is
  complete by default.
- **Channel → false.** Unchanged; expanding is opt-in.
- **An explicit argument always wins** — detected via
  `req.GetArguments()`, so `with_replies:false` on a DM still gives the
  lean read.

`isDMRef(ref)` decides from the reference **shape alone**, no API call:
`@handle` and bare `U…`/`W…` always open a DM, `D…` is one, a channel
name or `C…/G…` never is. Pure and unit-tested.

## Consequences

- "прочитай личку с X" returns the whole conversation, threads included,
  in one call — no second call with the flag.
- Channel cost profile unchanged; the extra `conversations.replies`
  calls happen only where thread count is inherently small.
- 593 → 594 tests. Minor release (1.22.0).

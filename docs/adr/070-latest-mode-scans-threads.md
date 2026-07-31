# ADR 070: latest-mode scans threads, not just the top level

Date: 2026-07-31
Status: accepted

## Context

Asking for the newest voice note in a DM (`transcribe_audio` /
`analyze_audio_tone` / `download_audio` with a channel and no
timestamp) failed with "no recent message with a matching attachment in
this conversation" while the note was plainly sitting in that DM,
posted minutes earlier.

Latest-mode reads `conversations.history`, which returns **top-level
messages only**. The note was a thread reply, so it was invisible to the
scan. Only the permalink path worked, because ADR 057's fallback already
walks the thread once a message is resolved.

This is the same blind spot fixed for digests and unread summaries:
Slack's history endpoint is not the conversation, it is the spine of the
conversation. Voice notes are especially prone to it because a reply is
the natural place to answer someone.

## Decision

When the top-level scan finds nothing, walk the recent threads:
`threadRoots` (pure) collects up to `threadScanLimit = 12` distinct
thread roots from the fetched history, newest-first, and each is opened
with `conversations.replies`.

Two details that are easy to get wrong:

1. **Pick by timestamp, not by first hit.** A match inside an older
   thread can be newer than a match in a younger one, so candidates are
   compared with pure `tsLess` rather than returning the first found.
2. **Honour `from`.** `selectLastFileMessage` gained a `fromUserID`
   parameter mirroring `selectLatestFileMessage`, so "my last voice
   note" cannot resolve to the other party's reply.

The thread walk runs on the fallback path only, so the common case still
costs exactly one history call.

## Consequences

- Latest-mode now finds notes wherever they were posted; the caller no
  longer has to hunt for a permalink to read the obvious thing.
- Worst case is 12 extra `conversations.replies` calls, and only when
  the conversation's top level genuinely has no matching attachment.
- Unparseable timestamps sort as oldest, so a malformed `ts` can never
  win the comparison and hand back the wrong file.
- 604 → 607 tests. Minor release (1.27.0).

# ADR 048: reach a voice note posted as a thread reply

Date: 2026-07-15
Status: accepted

## Context

A voice note (or any attachment) posted as a **thread reply** was
unreachable when the caller only held a permalink to the thread *root*
(the common case — you copy the link on the parent message, not the
reply):

- The root permalink resolves to the parent message, which itself has no
  attachment → `download_audio`/`transcribe_audio` returned "message has
  no file attachments".
- v1.7.0 latest-mode couldn't help either: it scans
  `conversations.history`, which does **not** include thread replies, so
  the reply is invisible to it.

Hit live: a pricing-negotiation thread where the decisive voice note was
a reply — the thread-root permalink couldn't reach it, and there was no
way to obtain the reply's exact `ts`.

## Decision

When the message a permalink/`ts` resolves to has **no matching
attachment**, fall back to scanning its **thread** for the newest
matching one, before giving up.

- `fetchFiles`: if `hasMatchingFile(msg.Files, accept)` the exact message
  is used as before. Otherwise it derives the thread root
  (`msg.ThreadTimestamp`, or the message's own `ts` when it isn't
  threaded) and calls the new `MessageService.LatestFileInThread`.
- `LatestFileInThread` runs `conversations.replies` (parent +
  replies, oldest-first) and returns the **last** message whose
  attachments satisfy `accept` — the newest — via the pure, unit-tested
  `selectLastFileMessage`.

The `from` filter stays **ignored** in permalink/`ts` mode (including the
thread fallback), honouring the documented contract that `from` only
applies to latest-mode.

## Consequences

- "read the voice note in this thread" now works from the thread-root
  permalink alone — no need for the reply's own `ts` or Copy-link. The
  motivating pricing thread is readable.
- `MessageClient` gains `LatestFileInThread`; fakes updated. Selection is
  a pure helper (`selectLastFileMessage`) tested for newest-reply-wins,
  non-matching-skip, and empty. 547 → 550 tests.
- Purely additive: a message that carries the attachment directly is
  unchanged; the thread scan only runs when the resolved message has no
  match (previously a hard error), so it strictly widens what resolves.
  One extra `conversations.replies` call only on that fallback path.
  Patch release (1.7.2).

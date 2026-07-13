# ADR 046: read the newest attachment straight from a conversation

Date: 2026-07-13
Status: accepted

## Context

ADR 045 let the audio/image tools resolve an attachment from a file URL,
closing the "I have no message ts" gap for a link you can right-click.
But the most common ask is even more direct: "read my last voice note in
this DM." There, you have no permalink and no file URL either — only the
conversation. `search.messages` doesn't index empty-text voice memos, so
there was no way to name the target without first hunting for a ts by
eye.

## Decision

Add a **latest-mode** to the shared `fetchFiles` front-half: when a
caller passes a `channel` but no `permalink` and no `timestamp`, resolve
"the newest matching attachment in this conversation" and download it.

- Conversation resolution (`Hub.resolveConversation`): a `@handle`
  channel means a DM — resolve the handle to a user
  (`UserService.IDForHandle`) and open the DM
  (`ChannelService.OpenDM`, idempotent `conversations.open`). Anything
  else defers to `ResolveID` (which already handles `#names` and
  canonical `C/G/D` IDs).
- Author filter (new `from` arg, `Hub.resolveAuthor`): empty = any
  author; `"me"` = the authenticated user (`Unread().Self`, needs a user
  token); otherwise a `@handle`. This is what makes "**my** last voice
  note" beat the other party's newer one.
- Selection (`MessageService.LatestFileMessage` →
  `selectLatestFileMessage`): `conversations.history` is newest-first, so
  the first message that carries an attachment the tool's predicate
  accepts (and, when `from` is set, was authored by that user) is the
  answer. A 60-message window bounds the scan.

All four tools (`download_audio`, `transcribe_audio`, `view_image`,
`analyze_audio_tone`) share `fetchFiles`, so the one change gives every
one of them latest-mode plus the `from` arg. The three resolution paths
(file URL, latest-mode, message) now converge on a single `finishFetch`
tail so they download and shape their result identically.

## Consequences

- "transcribe my last voice message in the DM with @teammate" works with
  no permalink and no ts — pass `channel:"@teammate"` (or the `D…` ID)
  and, for your own, `from:"me"`. This was the motivating request.
- Contracts grow: `UserClient.IDForHandle`, `ChannelClient.OpenDM`,
  `MessageClient.LatestFileMessage`; fakes updated. Selection and handle
  matching are extracted into pure helpers (`selectLatestFileMessage`,
  `matchHandle`) and unit-tested — newest-first, predicate skip, author
  filter, username-over-display-name precedence. 536 → 542 tests.
- Purely additive: a `channel` with a `timestamp` still takes the exact
  message path; existing callers are unchanged. `from` is ignored unless
  latest-mode is active. Minor release (1.7.0).
- `from:"me"` needs a user token (same requirement as the unread/status
  tools); without one it returns a clear "needs a user token" error
  rather than silently widening to all authors.

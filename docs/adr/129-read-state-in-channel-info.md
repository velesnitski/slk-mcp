# ADR 129: get_channel_info reports the operator's read state

Date: 2026-10-24
Status: accepted

## Context

A DM that showed up in a windowed sweep was missing from an unread-only
sweep, and there was no way to tell from the tools whether that was a
bug or whether Slack had the DM marked as read. The unread sweep reads
the read marker internally, but nothing exposed it, and `get_channel_info`
did not accept a DM by @handle at all — only names and C/G ids.

## Decision

- `get_channel_info` adds a read-state line, on by default with a user
  token (`read_state=false` skips it):
  "read state: last read <time> · newest <time> <author> · read | N unread".
- `UnreadService.ReadState` reads the marker with the **user** token:
  the bot's marker is a different one and says nothing about what the
  operator saw. It counts messages by others after the marker on the
  newest page of 100; the operator's own messages are never unread, as
  in the Slack client. A full page reports "N+ unread".
- No marker is reported as such, never as "read" or "unread": unknown is
  not either. Thread replies are tracked separately by Slack and are not
  counted; the line says so.
- `get_channel_info` resolves @handle and bare U… ids to the DM, like
  `get_channel_digest`, and labels a DM "@Name (DM)" instead of "#".

## Consequences

- "Is this DM really unread?" is one call.
- The line costs one conversations.info and one conversations.history
  call per request.

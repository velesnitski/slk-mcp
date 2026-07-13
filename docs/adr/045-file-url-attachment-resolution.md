# ADR 045: accept Slack file URLs in the audio/image tools

Date: 2026-07-13
Status: accepted

## Context

`download_audio` / `transcribe_audio` / `view_image` /
`analyze_audio_tone` all resolve an attachment through a MESSAGE — a
permalink (`…/archives/CID/pTS`) or channel + timestamp. That broke on a
real case: to validate your own voice memo you need its ts, but Slack's
`search.messages` does not index an empty-text voice memo, and the DM
digest doesn't expose the ts. The only handle you can get is the file's
"Copy link" — `…/files/<user>/<F…>/name.m4a` — which the message path
rejected ("not a slack permalink").

## Decision

Teach the shared `fetchFiles` front-half to accept a file URL. A new
`slack.ParseSlackFileURL` extracts the `F…` file ID; when present,
`fetchFiles` resolves the attachment directly via a new
`MessageService.FileInfo` (`files.info`) and downloads it, skipping
channel/message resolution entirely. Because all four tools share
`fetchFiles`, one change enables file-URL input across every one of
them; their `permalink` argument now documents both shapes.

The file-URL path reuses the same confined-temp-path download
(`confinedTempPath`, ADR 040) and predicate filter, so a wrong-type link
(e.g. an image URL passed to `transcribe_audio`) is reported, not
mis-handled. No new security surface: `files.info` returns the same
`url_private_download` a message would, downloaded with the server's own
token.

## Consequences

- "validate my voice message" now works from a right-click Copy-link,
  even when the message ts is unobtainable — the gap that motivated this.
- `MessageClient` gains `FileInfo`; fakes updated. New
  `ParseSlackFileURL` is unit-tested (file URL vs message permalink vs
  empty). 535 → 536 tests.
- Additive input (file URLs accepted in addition to permalinks); no
  behaviour change for existing callers. Minor release (1.6.0).

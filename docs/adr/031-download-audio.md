# ADR 031: `download_audio` — voice-message attachments for local transcription

Date: 2026-07-03
Status: accepted

## Context

Slack voice messages arrive as an `audio/mp4` (.m4a) file attachment on
an otherwise empty message. Every read tool renders that as an opaque
`[📎 file.m4a]` marker — the audio content is unreachable from the MCP
client, so a voice note is effectively invisible to any workflow built
on this server (digests, thread reads, knowledge-base ingestion).

Fetching the file out-of-band is worse than it sounds: `url_private`
requires the Slack token as a bearer header, which means exporting the
credential into a shell — exactly the pattern client-side security
tooling (rightly) blocks as potential exfiltration. The token should
never leave the server process.

Transcription itself does NOT belong in this server: speech-to-text
models are heavyweight, language-specific, and evolve independently
(whisper.cpp, mlx, cloud APIs). Slack's own transcript (when present)
is English-biased and unreliable for other languages.

## Decision

Add one tool, `download_audio` (permalink, or `channel` + `timestamp`;
`workspace`-aware via `workspaceTarget`, consistent with ADR 027/029):

1. Resolve the message: new `MessageService.MessageAt` — a
   `conversations.history` point lookup (`latest=ts, inclusive, limit 1`),
   falling back to `conversations.replies` because thread replies never
   appear in channel history.
2. Filter attachments to `audio/*` mimetypes. Non-audio files are
   reported as skipped, not errors — voice notes can travel with
   previews and the caller wants the sound.
3. Stream each audio file into `os.TempDir()` via new
   `MessageService.DownloadFile` (wrapping `GetFileContext`, which
   authenticates with the client's own token). Filenames are sanitized
   to `[A-Za-z0-9._-]` to keep paths shell-safe.
4. Return the local paths (+ mimetype, size) and a one-line hint to
   transcribe locally. Only paths cross the MCP boundary — never the
   token, never a URL that embeds authority.

The download loop lives in a free function over the `MessageClient`
contract (`downloadAudioFiles`), so tests drive the filter/IO behaviour
with a fake client and `t.TempDir()` — same seam pattern as
`operatorRepliedSince`.

## Consequences

- Voice messages become one tool call away from transcription; the
  client picks the STT engine and language (e.g. `whisper-cli -l ru`).
- The credential-handling surface stays exactly where it was: inside
  the server. No new env vars, no new scopes (`files:read` is already
  implied by the read scopes in the recommended manifest).
- `MessageClient` grows two methods; fakes updated. 474 → 483 tests.
- Temp files are not garbage-collected by the server — they live under
  the OS temp dir with a `slk-audio-` prefix and fall to OS cleanup.
  Acceptable for the interactive use case; revisit if unattended bulk
  use appears.

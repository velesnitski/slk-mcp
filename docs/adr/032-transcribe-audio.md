# ADR 032: `transcribe_audio` — run the STT pipeline server-side, toolchain optional

Date: 2026-07-03
Status: accepted

## Context

ADR 031 (`download_audio`) put voice-message audio within reach, but the
client still had to run `ffmpeg` + whisper by hand for every message —
three manual steps for what is conceptually one question ("what does
this voice note say?"). The obvious next step is to run the pipeline
under the hood.

The tension: whisper models are heavyweight, language-specific, and
evolve on their own release cadence. Baking an STT engine into the
server (CGo bindings, bundled models) would bloat a ~10 MB dependency-
free binary and marry it to one engine's ABI.

## Decision

`transcribe_audio` orchestrates HOST-provided binaries instead of
embedding an engine:

1. Reuse the ADR 031 front half (`fetchAudioFiles`: resolve message,
   download `audio/*` attachments with the server's token).
2. Resolve the toolchain — `detectSTT`: `ffmpeg` and `whisper-cli` from
   PATH, model from `~/.cache/whisper/ggml-small.bin`; each overridable
   via `SLACK_FFMPEG_BIN` / `SLACK_WHISPER_BIN` / `SLACK_WHISPER_MODEL`.
   Nothing becomes a hard dependency of the server.
3. Per file: `ffmpeg → 16 kHz mono WAV → whisper-cli -np -nt -l <lang>`.
   stdout and stderr are captured separately — whisper prints the
   transcript to stdout but spills model-loading noise to stderr, and
   mixing them would corrupt the transcript.
4. **Graceful degradation**: when any toolchain piece is missing the
   tool returns the downloaded paths plus an actionable install hint —
   i.e. it behaves exactly like `download_audio`. A missing local model
   must never make a voice message *less* reachable than 0.5.7 did.
   The hint is written for the *calling agent*: it carries the complete
   one-time install (brew + model download) and an explicit instruction
   that shell-capable clients may run it with user consent and retry —
   so a fresh machine self-heals in one round-trip. The server itself
   never executes installers: a Slack-facing process must not be able
   to invoke package managers.
5. Cleanup: transcribed audio (and the intermediate WAV) is removed —
   the transcript is the artifact. Files whose transcription failed are
   kept for manual retry, and the error carries the first stderr line.

`exec.LookPath` and the process runner are package-level seams
(`lookPath` / `runCommand`), so tests drive detection order, argument
construction, error paths, and empty-transcript handling without
executing anything.

## Consequences

- One tool call turns a voice note into text (language auto-detected or
  forced via `language`); the README how-to shrinks to "install the
  toolchain once".
- The server now shells out to host binaries for the first time —
  confined to this one tool, resolved through explicit config, never
  from request input (arguments are fixed flags + validated paths).
- `download_audio` stays: it is the degradation target, the manual
  escape hatch, and the tool for clients that bring their own STT.
- Config grows three optional env vars; no new scopes, no new deps.
  484 → 494 tests.

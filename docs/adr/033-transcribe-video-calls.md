# ADR 033: `transcribe_audio` accepts video — recorded huddles and clips

Date: 2026-07-03
Status: accepted

## Context

ADR 032 shipped transcription for voice notes (`audio/*`). But the
other spoken artifact in a Slack workspace is calls: a *recorded*
huddle (or a video clip) lands in the thread as a `video/mp4|webm`
file attachment — same `files` API, same `files:read` scope, already
downloadable by our pipeline. It was rejected only by the `audio/*`
mimetype filter, and `ffmpeg` — already in the toolchain — extracts an
audio track from video with the same command that converts voice notes.

Live (unrecorded) huddles are out of scope by physics: the audio is
never persisted, there is no file to fetch.

## Decision

- `downloadAudioFiles` takes an `accept func(goslack.File) bool`
  predicate; the filter becomes the caller's contract, not the loop's.
- `download_audio` keeps `isAudioFile` — its name is its contract.
- `transcribe_audio` uses `isTranscribableFile` = `audio/* || video/*`,
  and the ffmpeg step gains `-vn` so video inputs contribute exactly
  their audio track.
- `detectSTT` also resolves `ffprobe` (ships with every ffmpeg
  install) as a nice-to-have: each transcript header now carries the
  media duration ("2:13", "1:02:03"). A missing or failing ffprobe
  yields an empty duration, never an error — output enrichment must
  not create a new failure mode.

## Consequences

- Recorded huddles and video clips transcribe with the same one call
  as voice notes; the duration in the header makes long-call cost
  visible at a glance.
- A recorded hour-long huddle is a few hundred MB and transcribes in
  minutes on Apple Silicon with `ggml-small` — acceptable
  interactively, and the size/duration are printed so the caller can
  bail out for anything absurd.
- Whisper output on multi-speaker calls has no diarization — the
  transcript is a single stream. Good enough for "what was discussed";
  speaker attribution would need a different toolchain (revisit only
  on demand).
- 495 → 502 tests.

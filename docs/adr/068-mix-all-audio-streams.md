# ADR 068: mix every audio stream; download_audio accepts video

Date: 2026-07-31
Status: accepted

## Context

A 1:27 Slack video clip transcribed as three repeated tokens. ADR 067
correctly identified whisper hallucinating on a silent track, but the
operator then confirmed the recording DOES contain his voice. So the
silence was ours: something upstream produced a silent WAV.

Screen recordings and huddles routinely carry **more than one audio
stream** (system audio + microphone). Our conversion passed `-vn` and
let ffmpeg pick one "best" audio stream. When the mic sits on a stream
ffmpeg does not choose, the extracted track is silent while the
recording is not — and the pipeline reports silence (or, before 067,
hallucination) for a perfectly good file.

Diagnosis was also blocked: `download_audio` filtered on `isAudioFile`
and refused the same video permalink that `transcribe_audio` accepted,
so the file could not be pulled for inspection.

## Decision

1. **Mix all audio streams.** `audioStreamCount` (ffprobe, counted via
   pure `countNonEmptyLines`) drives the conversion: more than one
   stream → `amix=inputs=N:duration=longest:normalize=0`; one stream →
   the previous plain `-vn` command, byte-for-byte. Probe failure or a
   missing ffprobe reads as "not more than one", so the fallback is the
   old behaviour.
2. **`download_audio` accepts video** (`isTranscribableFile`). Refusing
   a huddle/clip with "no matching attachment" while `transcribe_audio`
   resolved the identical permalink was a contradiction the caller could
   not diagnose.

## Consequences

- Recordings whose voice lives on a second audio stream now transcribe;
  the 067 silence guard still catches genuinely silent input, and now
  measures the MIXED track, so it can no longer fire on a one-silent-
  stream file.
- `normalize=0` keeps levels unscaled, so amix cannot push a quiet mic
  further down toward the silence floor.
- 598 → 601 tests (stream counting, mix path, single-stream path
  unchanged). Minor release (1.25.0).

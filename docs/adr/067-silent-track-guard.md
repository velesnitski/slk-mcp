# ADR 067: refuse silent tracks instead of returning whisper hallucinations

Date: 2026-07-31
Status: accepted

## Context

`transcribe_audio` returned `"Песла Песла Песла"` for a 1:27 recording.
Forcing `language=ru` produced the same three tokens. That is not a
transcript: whisper HALLUCINATES on silence, emitting a few repeated
tokens that look exactly like a short, confident result. The source was
a screen recording whose microphone was never captured (the macOS
default records system audio or nothing).

The failure mode is the dangerous kind: the pipeline succeeded at every
step, so the caller had no signal that the output was fabricated. The
assistant nearly relayed those tokens back to the operator as the
content of his own recorded message.

## Decision

Measure the converted WAV before transcribing and refuse silent input.

- `meanVolumeDB` runs `ffmpeg -af volumedetect -f null -` on the 16 kHz
  mono WAV and parses `mean_volume` from stderr (`parseMeanVolumeDB`,
  pure and unit-tested).
- Below `silentTrackDB = -50 dBFS` the call returns an error naming the
  measured level and the likely cause (recording made without the mic),
  and whisper never runs.
- **Fail-open:** an unparseable or failed measurement returns
  `ok=false` and transcription proceeds. The guard only fires on a
  POSITIVE silence reading, so a probe quirk can never block real
  speech.

Speech, even quiet or far-mic, sits well above -50 dB mean; a track with
no recorded input measures -70 dB or lower, and digital silence reports
-91 dB.

## Consequences

- A silent recording now produces an explicit, actionable error instead
  of fabricated text presented as speech.
- Cost: one extra ffmpeg pass over an already-converted WAV.
- 595 → 598 tests (parse levels incl. fail-open, guard fires and stops
  whisper, unparseable probe does not block). Minor release (1.24.0).

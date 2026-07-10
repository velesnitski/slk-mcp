# ADR 044: native f0 (YIN) instead of aubio for `analyze_audio_tone`

Date: 2026-07-10
Status: accepted (supersedes the aubio pitch path in ADR 043)

## Context

ADR 043 shipped `analyze_audio_tone` with pitch as an OPTIONAL aubio
(`aubiopitch`) step. Checking the actual install footprint killed that
idea: on this Homebrew system `aubio` pulls **11 new formulae** — most
of the GCC toolchain (`gcc`, `isl`, `libmpc`, `mpfr`) plus `openblas` +
`numpy` + a stack of audio codecs — on the order of **1–2 GB** for one
number (f0). That is absurdly disproportionate, and it contradicts the
repo's dependency-minimal ethos (a ~10 MB static binary with ffmpeg as
its only host tool).

## Decision

Compute pitch **natively in Go**, using ffmpeg — already required — only
to decode:

- ffmpeg decodes the clip to raw mono 16-bit PCM (`-f s16le -`); the
  bytes come back on stdout via the existing `runCommand` seam.
- A compact **YIN** estimator (de Cheveigné & Kawahara 2002) runs
  in-process per 1024/512 frame: difference function → cumulative mean
  normalized difference → absolute threshold → parabolic refinement,
  bounded to the speech range (70–400 Hz), gated on frame RMS.
- Aggregated to **mean f0 + variability (std) + voiced fraction**. Pitch
  variability is a *second* arousal signal — agitated speech moves its
  pitch more — so it's reported alongside the mean.

The aubio binary lookup, the `aubiopitch` parser, and the "install
aubio" hint are removed. Zero new system dependencies; pitch is no
longer optional/absent — it just works wherever ffmpeg does.

## Consequences

- `analyze_audio_tone` now returns f0 (mean ± variability, voiced %) on
  any box with ffmpeg — no 1–2 GB toolchain, no optional path.
- YIN is unit-tested against synthetic tones (recovers 90/150/220/330 Hz
  within 2 Hz; silence → unvoiced) and the PCM decode is stubbed through
  the shared exec seam, so the whole pitch path is covered without audio
  files. 532 → 535 tests.
- Trade-off: YIN is monophonic and speech-tuned; it is not a
  general-purpose polyphonic pitch tracker (fine — a voice note is one
  speaker). Patch release (1.5.1): same tool, better engine, richer
  output; the tool's output text is non-contractual (ADR 037).

# ADR 069: never pass -nt to whisper; strip timestamps ourselves

Date: 2026-07-31
Status: accepted

## Context

A 1:27 video clip kept transcribing as three repeated tokens. ADR 067
(silence guard) and ADR 068 (multi-stream mixing) each addressed a
plausible cause, and neither fixed it — the guard never fired and the
file turned out to carry exactly one audio stream.

Measuring the pipeline instead of reasoning about it settled the
question. The extracted WAV was pristine: pcm_s16le, 16 kHz mono,
86.7 s, mean volume **-22.1 dB**, peak -0.3 dB. Running our exact
whisper command by hand reproduced the garbage — and did it in **1.0
second**, which is far too fast for 87 seconds of audio. Dropping flags
one at a time isolated it:

| flags | output | wall time |
|---|---|---|
| `-np -nt` (ours) | `Песла Песла Песла` | 0.8 s |
| `-nt` | `Песла Песла Песла` | 1.1 s |
| `-np` | full, correct transcript | 1.7 s |

`-nt` / `--no-timestamps` derails the decoder in whisper.cpp 1.9.x: the
run collapses into a couple of repeated tokens and terminates after the
first segment. It reads like a documentation-blessed way to ask for
clean text, which is exactly why it survived so long unquestioned.

The failure mode is the dangerous kind — not an error, but a short,
confident, well-formed string that a caller relays as if it were
speech.

## Decision

Drop `-nt`. Ask whisper for its normal timestamped segment listing and
convert it to flowing text in Go via pure `stripTimestamps`, which
removes `[hh:mm:ss.mmm --> hh:mm:ss.mmm]` prefixes and blank lines and
passes any other line through untouched.

A regression test asserts neither `-nt` nor `--no-timestamps` ever
appears in the whisper argument list, with the reason in the failure
message so the flag does not get re-added as a "cleanup".

## Consequences

- Long recordings transcribe in full instead of returning a plausible
  three-word lie.
- Keeping timestamps in whisper's output leaves the door open to
  surfacing per-segment times later; today they are stripped to preserve
  the existing plain-text contract.
- ADRs 067 and 068 stay: a genuinely silent track and a genuinely
  multi-stream recording are both real failure modes, they just were not
  this one.
- 601 → 604 tests. Patch-level behaviour fix, released as 1.26.0.

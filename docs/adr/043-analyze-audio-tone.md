# ADR 043: `analyze_audio_tone` — vocal tone from a voice message

Date: 2026-07-10
Status: accepted

## Context

`transcribe_audio` returns words, not delivery. A real question came up
that words can't answer: was a founder's voice message shouting or
evenly controlled? The transcript reads as harsh either way; the tone
changes how you respond (a blow-up to wait out vs a deliberate position
stated firmly). That signal lives in the audio, not the text.

## Decision

Add `analyze_audio_tone` (permalink or channel + timestamp;
workspace-aware). It reuses the ADR 031/041 download plumbing
(`fetchFiles`, `slk-tone` temp prefix, confined path) and orchestrates
host binaries — the same host-provided, degrade-gracefully model as
`transcribe_audio`:

- **ffmpeg (required)** — one `astats=metadata=1,ebur128` pass yields
  the crest factor (peaks over average) and, crucially, the **EBU R128
  loudness range (LRA)**.
- **aubiopitch (optional)** — mean f0 when present; absence is reported,
  never fatal.
- Missing ffmpeg → degrade to download-only + install hint.

The metric choice is the smart part. Phone voice notes are
auto-normalized, so **absolute loudness is meaningless** (a normalized
clip pins its peak at 0 dB whether whispered or yelled). What survives
normalization is the RELATIVE spread — LRA — and pitch. A controlled
delivery clusters low (LRA < ~5); an agitated or shouting one swings
wide (LRA > ~9). The tool maps LRA to a plain-language arousal band and
states the caveats inline (proxy, not an emotion model; read spread and
pitch, not the dB level).

Parsers are pure functions over ffmpeg stderr / aubio stdout (take the
LAST match — ffmpeg prints per-frame figures before the summary),
unit-tested against real trimmed output; the exec seam (`runCommand`,
`lookPath`) is shared with `transcribe_audio` so tests drive the whole
path with no binaries.

## Consequences

- "Was that voice message calm or a shout?" is one tool call. The
  answer for the message that prompted this: LRA 4.2 LU + ~129 wpm =
  controlled, not shouting — which reframes it as a deliberate position,
  not a tantrum.
- No new hard dependency (ffmpeg already powers transcribe); aubio is
  optional. Independent of whisper — tone works even without the STT
  model installed.
- Honest by construction: unparsed fields are omitted, not faked, and
  the proxy caveat ships in every result. Additive tool ⇒ minor release
  (1.5.0). 525 → 532 tests.

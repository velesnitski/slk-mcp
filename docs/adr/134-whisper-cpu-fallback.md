# ADR 134: retry whisper on the CPU when the GPU backend fails

Date: 2026-11-09
Status: accepted

## Context

After a Homebrew upgrade (whisper.cpp 1.9.5 on ggml 0.28), every
`transcribe_audio` call failed. whisper-cli aborted while loading the
model, before reading any audio:

    src/ggml-backend.cpp:1089: pre-allocated tensor (leaf_0) in a buffer
    (MTL0) that cannot run the operation (NONE)

The same binary with `-ng` (no GPU) transcribed the file normally. The
tool's error did not show this: it quoted the first line of whisper's
stderr, which is model-loading noise, so the failure read as an
unexplained crash.

## Decision

- When a whisper run fails, run it once more with `-ng`. If the CPU run
  succeeds, the GPU is marked broken for the life of the process and
  later files go straight to the CPU, so the crash is paid once, not
  per file.
- A run that fails on both backends leaves the GPU in use: a bad model
  or an unreadable file fails either way and says nothing about the GPU.
  The error then reports the GPU run's failure.
- Whisper's error names the cause: ggml's abort message
  (`file.cpp:NNN: …`) when present, else the first line naming an
  error, else the first line.

## Consequences

- A broken GPU build costs one failed load per server start and turns
  a working tool into a slower one instead of a broken one. On Apple
  Silicon the small model runs a 1–2 minute voice note on the CPU in
  well under a minute.
- A failed transcription costs up to two whisper runs.
- Nothing is configured: the fallback finds a working backend by
  itself, and an upgrade that fixes the GPU is picked up on the next
  server start.

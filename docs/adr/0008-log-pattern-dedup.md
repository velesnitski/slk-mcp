# ADR 0008: pattern dedup in log-channel mode

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0007 (log-channel mode).

## Context

v0.3.0 rendered each log channel as a severity histogram plus the
N most-recent messages per band. For channels where the same alert
fires repeatedly (zabbix triggers, gitlab pipeline retries, harbor
scan failures), the "recent ERROR" section turned into 3–10 nearly
identical lines that differed only by pipeline ID, hostname, or
timestamp. Operators (and the LLM consuming the digest) want
*distinct* alert kinds with a count, not a verbose list of repeats.

## Decision

Add a canonical-signature dedup pass between band binning and
rendering. Three new pieces in `internal/tools/dedup.go`:

- **`canonicalSignature(text)`** — normalizes a message body so
  similar alerts share a signature. Replacements run in a fixed
  order:
  1. `https?://...` → `<URL>` (URLs first, they contain digits/dots).
  2. IPv4 (`a.b.c.d`) → `<IP>` (stricter pattern than bare digits).
  3. Hex IDs of length 7+ → `<HEX>` (commit shas, uuids).
  4. Bare digit runs → `<N>`.

  Whitespace is collapsed and the result lowercased so `"FATAL: x"`
  and `"fatal: x"` merge. Truncated to 200 chars to keep the dedup
  map cheap on pathological inputs.

- **`dedupLogSamples(messages, maxGroups)`** — groups by signature,
  keeps the most-recent representative per group, sorts by count
  descending then by recency, and returns the top `maxGroups`
  patterns plus a `remainder` count of dropped messages.

- **`format.LogPattern{Sample, Count, Signature}`** — pattern
  carrier with the signature retained for tests / debugging.

`format.LogBand` gains a `Patterns []LogPattern` field. Renderer
prefers `Patterns` when present; falls back to the legacy
`Samples []Message` path so older callers keep working.
`buildLogBands` now populates `Patterns`. Pattern lines render with
a "(×N similar)" suffix when `Count > 1`; counts of 1 stay clean.

## Why this signature scheme

Hex IDs (commit shas, uuids without dashes) are noisy in CI alerts.
But replacing all hex would mangle ordinary words containing hex
chars — `cafe`, `deed`, `face`, `fade`. The minimum-7-char rule
hits real shas while leaving English words alone.

URL replacement runs first because the URL regex is greedier than
the IP regex (URLs can contain IP-shaped substrings on internal
networks: `https://10.0.0.1:8080/path`). Without that ordering,
the IP regex would carve out `<IP>` first and the URL regex would
miss the now-fragmented URL.

IPs run before bare digits for the same reason: bare-digit
replacement on `192.168.1.5` would produce `<N>.<N>.<N>.<N>`, which
preserves no semantic information and over-merges unrelated alerts.

## Trade-offs

- **Over-merge risk**: distinct alerts that share boilerplate but
  differ only in numeric details (e.g. error codes) will merge.
  Example: `"HTTP 500 from /a"` and `"HTTP 503 from /b"` produce
  different signatures (`/a` vs `/b`), so codes that *also* differ
  by path stay separate. Codes that differ only in the number
  (`HTTP 500` vs `HTTP 503` from same path) merge — and that is
  usually fine: same endpoint flapping between 5xx codes is one
  story, not two.
- **Under-merge risk**: alerts that differ by alphabetic detail
  (`"high cpu on dc1"` vs `"high cpu on dc-1"`, hyphen vs no
  hyphen) get distinct signatures. This is conservative on
  purpose — the cost of leaving two near-duplicate lines is much
  lower than collapsing two genuinely different incidents.
- **Pattern semantics changed silently for callers that build
  `LogBand` themselves with `Samples`** — they keep getting the
  legacy per-message rendering. The fallback is documented in
  `format.LogBand` comments.

## Consequences

- A typical zabbix-alert channel with 30 messages (1 alert × 25,
  3 distinct other × 5) now renders ~4 lines instead of 30.
  Estimated 80% additional token reduction on top of the 70%
  saved by v0.3.0.
- `samplesPerBand` parameter (now meaning "max distinct patterns
  per band") keeps the same default (3) and the same MCP-tool
  parameter name. The semantic shift is documented in the
  CHANGELOG.
- Test coverage: `internal/tools/dedup_test.go` adds 33 new cases:
  per-regex behaviour (numbers, IPv4, URLs, hex IDs), the family-
  merge invariant, distinct-alert isolation, lowercase + whitespace
  collapse, truncation, recency tiebreak, top-N + remainder math,
  and renderer behaviour with both `Patterns` and legacy
  `Samples` paths.

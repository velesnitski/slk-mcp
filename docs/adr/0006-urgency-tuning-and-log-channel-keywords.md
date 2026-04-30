# ADR 0006: tunable urgency + en-us log-severity keywords

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0005 (urgency heuristic).

## Context

Two follow-up needs after v0.2.7 shipped:

1. **Per-call tuning.** Different triage modes want different urgency
   sensitivity. A morning-recap call benefits from amplified urgency
   (so incidents jump up); a "just show me the digest" call may want
   urgency dampened so volume comparisons stay readable. Operators
   also have domain-specific terms ("p0", "prod down", "internal tool")
   that the built-in en+ru list cannot anticipate.
2. **Log / alert channels are mostly English.** Bot-driven channels
   (monitoring, ci, registry, cloud) emit messages like "ERROR — service
   unreachable" or "GitLab pipeline #1234 failed on stage build".
   The v0.2.7 built-in list focused on conversational urgency
   keywords and missed these severity terms, so log channels with
   real failures sat below noisy human channels in the digest.

## Decision

Two new MCP parameters on `get_unread_summary`, plus an extended
built-in keyword list:

- `urgency_weight` (number, default `1.0`) — multiplier on the raw
  urgency score before it is folded into `rankUnread`. Treats
  `0` and negative values as "use the default" so that the
  zero-struct sentinel in tests still means defaults; pass `0.5`
  to dampen, `2.0` to amplify.
- `urgency_keywords` (string, default `""`) — comma-separated
  additional keywords. Parsed via `parseExtraKeywords` (lowercased,
  trimmed; empty / whitespace-only entries dropped to avoid the
  `strings.Contains(_, "")` always-true trap). Additive to the
  built-in list — domain extras stack on top, they don't replace.

Built-in keywords gain an English log-severity block:
`error`, `errors`, `failed`, `failure`, `fatal`, `alert`,
`exception`, `panic`, `outage`, `timed out`. Plus the Russian
`не отвечает` for symmetry. Deliberate omissions:

- `down` — would match `downloaded`, `downstream`, `markdown`,
  `breakdown`, `cooldown` etc. The cost (random noise across human
  chat about repos and design docs) outweighs the benefit.
- `fail` — superset issues; `failed` and `failure` cover the
  cases that matter without doubling on `failed/failure` text.

## Consequences

- Calling `get_unread_summary` with no params still works exactly
  the same — `urgency_weight=0` is interpreted as the default 1.0.
- A typical zabbix / gitlab failure message ("FATAL: connection
  refused", "pipeline failed", "AWS outage") now scores 10–20
  urgency without any caller configuration. Multiple failures
  in one channel stack: a log channel with 5 distinct error
  messages outranks a human channel of similar volume.
- Operators with niche stacks can pass their own product /
  internal-service names through `urgency_keywords` without
  redeploying.
- Test coverage: `internal/tools/urgency_test.go` adds 18 new cases —
  weight scaling (×0, ×0.5, ×1, ×2, negative), extra-keyword
  additivity, the empty/whitespace-skip invariant,
  `parseExtraKeywords` (CSV trim/dedup, Cyrillic input), and
  log-severity coverage (10 sample log lines covering positive
  and negative cases including `succeeded` and `merged MR`
  which must NOT fire).

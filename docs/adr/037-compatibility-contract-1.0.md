# ADR 037: 1.0.0 — the compatibility contract

Date: 2026-07-06
Status: accepted

## Context

slk-mcp has run the author's entire daily Slack workflow across two
workspaces for months: 18 tools, 500+ tests, 36 ADRs, CI-green, and a
tool surface that has only grown additively through the 0.5.x line. The
question is whether to cut 1.0.0 — and, more importantly, what 1.0
should *promise*, since an unqualified 1.0 for an MCP server implies
API stability that must be defined before it can be kept.

Two things gated a clean 1.0 and are now resolved:
1. The read surface was non-uniform (two digests lacked `workspace`) —
   fixed in ADR 036.
2. "Stable output" is ambiguous for a tool whose output is prose read
   by an LLM, not parsed by code. v0.6.0 literally added a `cursor:`
   line to the unread header; treating rendered text as contractual
   would have made that a breaking change, which is absurd.

## Decision

Cut **1.0.0** and define the contract explicitly. SemVer governs the
**machine surface**; the **rendered text is expressly non-contractual**:

**Covered by SemVer (breaking changes ⇒ major bump):**
- Tool **names** and their existence.
- **Argument** names, types, defaults, and required/optional status.
- The **meaning** of a tool call (what it fetches / mutates) and its
  error-vs-success disposition.
- Environment-variable names and their semantics.

**NOT covered (may change in any minor/patch):**
- The exact **wording, layout, or ordering of rendered output** —
  digests, summaries, headers (including the `cursor:` line), section
  labels, truncation markers. This text targets an LLM; it will keep
  evolving for density and clarity.
- Log lines and debug output.
- Internal package structure under `internal/`.

Corollary for callers: never regex the human-readable body. The
machine-readable handles are the typed arguments in and the documented
structured tokens out (permalinks, `cursor:` ts, issue IDs under
`include_refs`) — those move under SemVer; the prose around them does
not.

Version starts at 1.0.0 (the unreleased 0.6.0 delta-cursor work folds
in — it never shipped to main). `main.go` var, the `/mcp` label sync,
and CHANGELOG all move together.

## Consequences

- Users get a real stability promise on the surface they actually
  integrate against (tool + arg schemas), while the team keeps full
  freedom to improve output density — the single most-iterated part of
  this server.
- Future output-only changes (better huddle collapsing, new digest
  modes) ship as minor/patch, honestly. A tool rename or arg-type
  change now correctly demands a 2.0.
- The contract is documentation, enforced by review, not code — the
  cheapest thing that could possibly work, consistent with the rest of
  the ADR log.

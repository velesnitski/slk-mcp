# ADR 038: workspace-aware `list_users` (bug fix)

Date: 2026-07-07
Status: accepted

## Context

Found live one day after 1.0.0: calling `list_users` with
`workspace: "secondary-label"` silently returned the PRIMARY
workspace's users. The tool never declared a `workspace` argument, and
MCP hosts pass unknown arguments through without error, so the caller
had no signal the scope was ignored. It was the single reader missed by
the ADR 027/029 workspace pass — and by the ADR 034 review, whose grep
keyed on the scoping triple that this tool (no scoping at all) never
contained. Lesson recorded: audit by *tool inventory*, not by code
pattern.

## Decision

`list_users` gains `workspace` with the same fan-out contract as its
direct sibling `list_channels` (ADR 027): empty = every workspace, one
`## [label]` section each; a named label = that workspace, flat output;
unknown label = error. The body moved to a scoped `listUsersBody`, so
`with_activity`'s per-user `search.messages` fan-out now runs against
the SAME workspace's search index — under the old code a
secondary-workspace listing would have produced primary-scoped (wrong)
last-post dates.

Under the ADR 037 contract this is a minor release (1.1.0): the new
argument is additive, and the old behaviour of ignoring an undeclared
argument was a bug, not a promise. The multi-workspace default (empty
arg now merges all workspaces instead of listing the primary only)
matches the documented read-sweep convention.

## Consequences

- All 18 tools are now workspace-correct; none accepts-and-ignores.
- Single-workspace deployments are byte-unchanged.
- 513 → 514 tests (unknown-label routing pinned).

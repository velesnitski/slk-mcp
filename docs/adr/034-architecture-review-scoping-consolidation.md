# ADR 034: architecture review — consolidate workspace scoping and permalink resolution

Date: 2026-07-06
Status: accepted

## Context

A deliberate architecture pass over the codebase (~15k LoC, 9 packages)
after the 0.5.x feature run. The layering is healthy and stays as is:

    main → tools (Hub) → slack services → ratelimit
                       ↘ format / digest (pure render, no I/O)

- `contracts.go` narrow interfaces + compile-time assertions are the
  test seam and stay the pattern for new service methods.
- `withClient` shallow-copy scoping is the multi-workspace mechanism;
  it composes with everything and has no known sharp edges.
- `wrap()` middleware is a pass-through, so the registration
  split-brain (table-driven `register`/`toolDef` pilot in search.go vs
  raw `s.AddTool` closures everywhere else) is *cosmetic*, not a
  correctness gap. Migrating ~16 tools to the table would be churn with
  no behavioural payoff; the dual style is hereby blessed until a real
  middleware need (timing, panic recovery) forces the migration.

Two genuine smells had accumulated through ADRs 027–033, both of the
same species — a behavioural contract living as N copies:

1. **Workspace-scoping triple, ~10 copies.** Every single-target
   handler spelled out `workspaceTarget → nil-check(unknownWorkspaceMsg)
   → withClient`. Ten copies of one contract is drift waiting to happen
   (a new tool forgetting the nil check compiles fine and panics on an
   unknown label).
2. **Permalink fill-in, 4 copies.** get_thread, mark_read, and
   fetchAudioFiles each re-implemented "explicit args win, permalink
   fills the gaps" — and the error text had *already* drifted
   ("permalink could not be parsed" vs "invalid permalink").

## Decision

Two helpers in hub.go, both mechanical extractions:

- `scopedWorkspace(name) (scoped *Hub, wsName string, errRes *mcp.CallToolResult)`
  — the one true spelling of the triple. Replaces all ten sites.
- `resolveMessageRef(permalink, channel, ts, useThreadTS) (channel, ts, errRes)`
  — the shared fill-in contract; `useThreadTS` selects thread root
  (get_thread) vs message ts (mark_read, audio) and names the field in
  error messages. Replaces three sites.

`runDeleteMessage` deliberately does NOT adopt resolveMessageRef: its
documented contract is different (permalink *overrides* explicit args
and short-circuits channel-name resolution with the canonical ID). The
variant is now called out in both places instead of looking like an
accidental fourth copy.

Reviewed and left alone, with reasons:
- `format.go` (764 LoC) and `unread.go` (666 LoC) are large but
  cohesive single-responsibility files; splitting them would trade
  greppability for file count.
- `get_multi_channel_digest` / `get_morning_recap` are not
  workspace-aware — a *feature* gap (they sweep the primary only), not
  a refactor, so it is recorded here and not smuggled into a
  no-behavior-change commit.
- gofmt drift in a handful of files predates this pass and is not CI-
  enforced; left untouched to keep this diff reviewable.

## Consequences

- Net −60 lines of handler boilerplate; a new single-target tool is
  now two lines from being workspace-correct, and the unknown-label
  behaviour can only be changed in one place.
- Minor user-visible normalization: audio's combined "provide a
  permalink, or channel + timestamp" error became the shared
  field-specific messages. No tool schemas changed; no version bump.
- All 502 pre-refactor tests pass unmodified — the refactor is
  behaviour-preserving by construction; 2 new tests pin the helpers.
  502 → 504 tests.

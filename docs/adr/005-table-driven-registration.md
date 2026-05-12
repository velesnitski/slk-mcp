# ADR 005 — Handler migration to `Hub.X()` accessors + table-driven registration

**Status:** accepted
**Date:** 2026-05-12
**Tag at acceptance:** v0.4.3

## Context

Two paper tigers from v0.4.0 / v0.4.1 sat in the codebase doing
nothing:

1. **The `Hub.Users()` / `Channels()` / `Messages()` / `Search()` /
   `Unread()` accessors** (introduced by ADR 003) returned the
   narrow `UserClient` / `ChannelClient` / ... contracts from
   `contracts.go` — but every handler still reached through the
   concrete `h.client.Users.X()`. The interface seam was compile-time
   only, with no consumer actually exercising it. Tests using the
   `fakeUsers` substitution proved the pattern *worked* but no
   production code path was *using* it.
2. **The `toolDef` / `register` / `wrap` table seam** (also from
   ADR 003) was carrying `//nolint:unused` directives because no
   register* method called it. The directives were a smell: either
   the seam earns its keep or it should be deleted.

Both were deliberate parking spots — "the next pass will use them" —
but the next pass kept deferring. The longer they idled, the more
likely a refactor would forget they exist.

### Options considered

- **a.** Delete both. Cheapest; loses a documented design intent
  that already shipped in an ADR.
- **b.** Migrate everything at once. Riskiest; touches every handler
  file and every register* method in one diff.
- **c.** Migrate the consumer side broadly (every `h.client.X.Y()`
  call site) and pilot the table seam on one register* method as
  proof-of-fit. Leaves a clear, reviewable footprint for the
  remaining register* methods.

## Decision

Use **(c)**:

- All 33 `h.client.{Users,Channels,Messages,Search,Unread}.Method(...)`
  call sites became `h.X().Method(...)`. The accessors return
  the interface from `contracts.go`, so every handler now consumes
  the narrow contract instead of the concrete service.
- `channelDisplayLabel` (one consumer that passed the service by
  value, not by method call) was broadened from
  `*slack.UserService` to `UserClient`. `Name(ctx, id) string` was
  added to `UserClient` to support that consumer; the compile-time
  assertion in `contracts.go` continues to enforce that
  `*slack.UserService` satisfies the broader interface.
- `registerSearchTools` was rewritten to use
  `Hub.register(s, toolDef{...}, toolDef{...})`. Two handlers were
  extracted to `handleSearchMessages` / `handleFindDecisions` —
  named methods are now easier to test, instrument, and reference
  in stack traces than inline closures.
- The `//nolint:unused` directives on `toolDef` / `register` /
  `wrap` were removed; the seam is now load-bearing.

The other register* methods stay in their current shape pending
incremental migration. `search.go` is the reference for what the
target looks like.

## Consequences

- The `UserClient` / ... contracts now have a real consumer.
  Drift between the interface and the concrete service breaks the
  build in two places (the compile-time `var _ Iface = ...` and the
  actual handler call), making divergence loud.
- Adding a new method to a service now requires explicit thought:
  do we expose it on the contract (and break test fakes that need
  to update) or use it only internally to `internal/slack`? The
  ergonomic cost is a feature, not a bug.
- `register` centralises the disabled / read-only / user-token gate
  in one place. New tools added through this path can't accidentally
  ship without one of those checks — they're a property of the
  registration table, not of each handler.
- `wrap` is still a pass-through. The next time we need request
  timing or panic recovery, the migration is a single file change
  instead of touching every register* method.
- Tests in `internal/tools/` increase in value: `fakeUsers` (and
  similar fakes that will follow) now drive real production code
  paths, not just contract-shape assertions.

## Validation

- `go build ./...` and `go test ./...` both clean after migration;
  329 tests pass (same as before).
- The compile-time assertion block at the bottom of
  `internal/tools/search.go` pins `handleSearchMessages` and
  `handleFindDecisions` to the `server.ToolHandlerFunc` shape so
  upstream signature drift in `mark3labs/mcp-go` breaks the build
  here, not in a downstream call.
- `contracts_test.go` now drives `Name` through the fake, exercising
  the broadened interface end-to-end.

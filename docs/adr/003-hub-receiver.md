# ADR 003 — `Hub` receiver replaces `Deps` service-locator

**Status:** accepted
**Date:** 2026-05-12
**Tag at acceptance:** v0.4.0

## Context

Through v0.3.x, every tool register function had the same shape:

```go
func registerThreadTools(s *server.MCPServer, d Deps) {
    s.AddTool(... , func(ctx, req) {
        x := d.Client.Users.NamesFor(ctx, ...)
        if d.Cfg.IsDisabled(...) { ... }
        d.Log.Info(...)
        ...
    })
}
```

`Deps` was a three-field struct (`Client`, `Cfg`, `Log`) threaded
through every register function and every helper that touched
shared state. By v0.3.26 it was passed to 9 free functions and many
closures.

This is the **service-locator pattern**. It works, but:

1. Every cross-cutting concern (timing, panic recovery, structured
   logging per tool call) has to be plumbed by hand into every
   handler.
2. The struct keeps growing — every new dependency means another
   field.
3. Test substitution requires constructing the full struct even
   when a test cares about one service.
4. Discoverability: "what tools exist?" requires reading 6 register
   functions instead of asking the receiver.

### Options considered

- **a.** Keep `Deps`; document the discipline. Lowest churn, no
  middleware seam.
- **b.** Method-receiver pattern — `Hub` owns the dependencies,
  every register* becomes `(h *Hub) registerX`. Idiomatic Go;
  cross-cutting concerns become method decorators
  (`(h *Hub) wrap(name, handler)`).
- **c.** Full dependency-injection framework (wire, fx, etc.).
  Massive over-engineering for a single binary with three
  dependencies.

## Decision

Use **(b)**. `internal/tools/hub.go`:

```go
type Hub struct {
    client *slack.Client
    cfg    *config.Config
    log    *slog.Logger
}

func NewHub(client *slack.Client, cfg *config.Config, log *slog.Logger) *Hub
func (h *Hub) RegisterAll(s *server.MCPServer)
func (h *Hub) wrap(name string, fn server.ToolHandlerFunc) server.ToolHandlerFunc
```

`main.go` calls `tools.NewHub(client, cfg, log).RegisterAll(srv)`.
Pure helpers (`parseChannelList`, `collectUserIDs`, `mergeRefs`,
`detectDecisions`, `matchDecision`) stay as free functions in
`register.go` — making them methods on `*Hub` would imply they
read shared state, which they don't.

A `toolDef` table type and `(h *Hub).register(s, defs...)` method
exist as the seam for future table-driven registration. Today's
handlers still use `s.AddTool` directly; the seam is in place so a
future refactor can centralise the
`IsDisabled / ReadOnly / RequiresUserToken` filter checks.

The `wrap()` decorator is a deliberate pass-through today. It
exists so timing / panic-recovery / structured-log middleware can
be added in one place without touching individual handlers.

## Consequences

- Handlers read `h.client`, `h.cfg`, `h.log` (private fields) —
  shorter and tied to a receiver.
- Adding a dependency means adding a Hub field plus updating
  `NewHub`. Not a struct that grows in surprising ways.
- Future telemetry / OpenTelemetry tracing can be installed via
  `wrap()` without diffs across every handler.
- Migration was mechanical: ~9 free functions converted, ~30
  internal call sites updated. Pure refactor; behaviour unchanged.
- Trade-off accepted: tests that want to substitute one service
  still construct a full `*slack.Client` (or use the contracts in
  ADR follow-up; see `internal/tools/contracts.go`). The
  interfaces are declared; the substitution wiring is left to the
  test author when needed.

## Validation

323 existing tests passed unchanged across the Hub migration —
`*Hub` field accesses are the only behavioural surface, and
nothing in the test suite depended on `Deps` directly.

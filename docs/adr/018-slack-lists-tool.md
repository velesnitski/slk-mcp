# ADR 018 — `get_list_items` for Slack Lists via raw HTTP

**Status:** accepted
**Date:** 2026-05-29
**Tag at acceptance:** v0.4.16

## Context

Slack Lists are the new structured-table feature. Each list lives
at `https://<workspace>.slack.com/lists/<team>/<F-id>` and is, under
the hood, a file with an `F`-prefix ID. A user pinged me a list URL
and asked for it to participate in the daily digest — same way
`get_unread_summary` and `get_mentions` already do.

Two facts shaped the design:

1. **The relevant Slack endpoint is `slackLists.items.list`** (POST,
   JSON body, Bearer auth). It is new enough that **slack-go v0.15.0
   has no helper for it**. A grep across the vendored package finds
   only `stars_test.go` matching `*list*` patterns; nothing under
   `slackLists`.
2. **`lists:read` is a user-scope only.** Bot tokens cannot carry
   it. So the tool needs to refuse to register without a user token
   and surface a precise install-side action when the scope is
   missing.

### Options considered

- **(a)** Wait for slack-go to add Lists support, then wrap the
  helper as we do for History/ThreadReplies. Open-ended timeline,
  blocks the use case indefinitely.
- **(b)** Subclass `goslack.Client` and call its internal HTTP
  machinery through reflection. Brittle, depends on private
  symbols, breaks on any slack-go upgrade.
- **(c)** Speak raw HTTP from a new `ListService`. Slack's public
  API base (`https://slack.com/api/`) is already a documented
  constant; the endpoint is documented; the JSON shape is
  documented. The only thing we lose by skipping slack-go is the
  `RateLimitedError` translation, which we recover by detecting
  `429` ourselves and wrapping into the same error type so any
  future `ratelimit.DoR` integration is a one-line change.

## Decision

Use **(c)**.

### Service shape

```go
type ListService struct {
    token    string
    http     *http.Client
    log      *slog.Logger
    BaseURL  string // overridable for tests
    Endpoint string // overridable for tests
}

func (s *ListService) HasToken() bool
func (s *ListService) Items(ctx, listID, cursor string, limit int) (*ListItemsResult, error)
```

- `BaseURL` / `Endpoint` are exported on the struct so tests inject
  an `httptest.Server`. Production code calls `newListService` once
  during `slack.New` with the defaults baked in.
- Token is held as a string (not a `*goslack.Client`) because all
  we need is a single header; routing through goslack would buy
  nothing.

### Tool shape

```go
toolDef{
    Name:              "get_list_items",
    RequiresUserToken: true,
    Handle:            h.handleGetListItems,
}
```

The existing `RequiresUserToken` gate (see ADR 005) prevents the
tool from even registering when only a bot token is present —
better failure mode than `missing_scope` at first call.

### Decoder strategy

Slack Lists schemas are user-defined: a row's cells are arbitrary
columns of type text/date/select/person/etc. The decoder keeps a
per-item `Raw map[string]any` AND extracts the fields we render,
plus a `bestEffortTitle` heuristic (explicit `title` cell → first
non-empty cell → row id → item id). This lets `get_list_items`
return readable output for any list without the caller knowing the
schema, and leaves the raw payload available for future structured
renderers.

### Failure-mode coverage

| Failure | Behavior |
|---|---|
| No user token | `ErrListsNoToken` sentinel; tool not registered |
| `list_id` empty / whitespace | `errors.New("slack-lists: list_id is required")` |
| `lists:read` missing on token | Slack's `missing_scope` surfaced verbatim |
| Malformed JSON body | Parse error includes the first 200 bytes for debugging |
| 429 Too Many Requests | `*goslack.RateLimitedError` with `Retry-After` parsed |
| Other HTTP error | Wrapped as `slack-lists: ... %w` |

## Consequences

- **New capability** with a contained surface: one service, one
  tool, no contracts disturbed elsewhere. Existing `MessageClient` /
  `UnreadClient` are unchanged.
- **Operator action required** to actually use this in production:
  re-authorize the Slack app installation with `lists:read` added
  to the scope set, swap the new `xoxp-…` token into
  `SLACK_USER_TOKEN`. Without that step the tool refuses to register
  (no user token) or surfaces `missing_scope` (token present but
  short on scope). Both modes are explicit.
- **Raw HTTP debt is bounded.** If slack-go adds Lists support
  later we swap the inside of `Items` for the SDK call; the public
  signature stays. The decoder logic (`hydrateListItem`,
  `bestEffortTitle`, `displayValue`) is reusable.
- **No retry-in-loop yet.** Lists API is low-volume; the tool is
  user-invoked, not background. Wrapping in `ratelimit.DoR` is a
  trivial later change since `Items` already returns
  `*goslack.RateLimitedError` on 429.

## Validation

- `go vet ./...` — clean.
- `go test -race -count=1 ./...` — 421 pass (was 403; +18 from the
  lists suite plus incidental coverage).
- Sensitive-string scan over touched files — no real product names,
  workspace identifiers, channel handles, or peer names leaked into
  ADR, CHANGELOG, or test fixtures. Test fixtures use synthetic
  `F0ABC1234DE` placeholder, the same generic shape Slack documents.
- Manual smoke (not part of CI):
  1. `go build -o slk-mcp .`
  2. `SLACK_USER_TOKEN=<scoped-token> ./slk-mcp -version` →
     `0.4.16`.
  3. Call `get_list_items list_id=F…` against a real workspace
     with `lists:read` enabled. Either renders items or returns
     `missing_scope` if the scope was not added — both are
     actionable.

## Out of scope

- Write side (`slackLists.items.create / update / delete`). Adding
  these requires `lists:write` and a separate decision on whether
  slk-mcp should be allowed to mutate list state from a digest
  context. Easy to add later when there is a concrete workflow.
- A typed schema layer that knows about Slack's list column types
  (date, select, person, etc.). Today the renderer collapses
  everything to strings. A typed layer is justified only if a
  consumer needs column-aware filtering (`due_date < today`); not
  the case yet.
- A `MessageClient`-style cache. List rows churn slowly but read
  patterns are interactive — a 1-call latency is acceptable, a
  cache layer is premature.

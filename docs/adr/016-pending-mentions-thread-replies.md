# ADR 016 — `pending_only` mention filter must also check thread replies

**Status:** accepted
**Date:** 2026-05-28
**Tag at acceptance:** v0.4.14

## Context

`get_mentions(pending_only=true)` is the user-driven escape hatch for
"which @-pings haven't I answered yet?" — useful when the digest is
long and the operator wants to triage. The contract is documented in
the tool description as:

> Only keep mentions where you haven't posted a text reply afterwards
> (emoji reactions and file uploads don't count).

The implementation, until this ADR, satisfied that contract with a
single `conversations.history` call per match: oldest = mention.ts,
limit = 20, scan for the operator's user id with non-empty text.

Reported failure (synthetic shape that reproduces the original
report):

```
DM channel D_X, thread root T at ts = R
  ts = R           U_PEER     "thread root"
  ts = R + 0.001   U_PEER     "ping <@U_SELF> question"   ← mention
  ts = R + 0.002   U_SELF     "yes, on it"                ← reply
```

The operator's reply at `R + 0.002` is inside the same thread as the
mention. `conversations.history` for the DM returns the thread root
(`R`, U_PEER) and nothing else — both the peer's mention and the
operator's reply live as thread replies and are excluded from the
top-level history endpoint by design.

Result: `pending_only=true` keeps the mention as pending even though
the operator clearly answered it. False positives in the
already-handled bucket dilute the signal exactly when the operator
needs it most.

### Two cases under the same root cause

Once the gap was understood, a second case fell out of the same
logic — covered by the fix in the same pass:

- **(A)** Mention is itself a thread reply (its `permalink` carries
  `?thread_ts=<root>`). Operator's reply is also in the thread.
- **(B)** Mention is top-level; operator answered by *opening* a
  thread on the mention. The reply lives inside that thread.

`conversations.history` misses both. Only `conversations.replies`
on the correct thread root surfaces them.

### Options considered

- **a.** Bump `pending_only`'s history `Limit` from 20 to something
  much larger. Doesn't help — thread replies are not in history at
  any limit; the endpoint is shaped that way intentionally.
- **b.** Gate the thread sweep on `ExtractThreadTS(m) != m.Timestamp`
  (i.e. only when the permalink advertises a thread). Closes Case A
  but leaves Case B open: a top-level mention has no `thread_ts` in
  its permalink, yet the operator may still have answered it inside
  a thread.
- **c.** Always run a second pass against `conversations.replies`
  with `ts = ExtractThreadTS(m)` (which falls back to the mention's
  own timestamp for top-level messages). Closes both cases. For a
  top-level mention with no thread, the call returns the parent
  alone — cheap, no extra side effects, no error.

## Decision

Use **(c)** with one optimisation: short-circuit if the top-level
history scan already found the operator's reply. That keeps the
common path (operator replied at top level) at one API call, and
spends the second call only when the top-level scan was empty.

Concretely:

1. Extract a free function `operatorRepliedSince(ctx, msgs, log, m,
   selfID)`. It takes the `MessageClient` directly instead of going
   through `*Hub`, so tests can drive it without an HTTP layer.
2. Step 1 inside it: `History(channel, oldest = mention.ts)`, scan
   for a newer operator-authored non-empty text message. Hit ⇒
   return early.
3. Step 2: compute `threadTS = format.ExtractThreadTS(m)`. Fetch
   `ThreadReplies(channel, threadTS)`. Same scan rule against the
   replies. Hit ⇒ return.
4. Otherwise the mention is genuinely pending.
5. `Hub.operatorReplied(ctx, m, selfID)` becomes a one-line wrapper
   that supplies `h.Messages()` and `h.log`. The pipeline in
   `filterPendingMentions` is unchanged apart from passing the whole
   `SearchMessage` (so the permalink survives the call boundary).

## Consequences

- **Filter contract restored.** The "text reply afterwards" rule now
  applies whether the reply is top-level or in a thread.
- **Worst-case API cost per mention doubles** (history + replies)
  but only when the operator did not reply at top level. The worker
  pool stays at 4, the rate-limit wrapper is unchanged, and
  `pending_only` is opt-in. No measurable change for callers who do
  not use the flag.
- **No new failure modes for absent threads.** Calling
  `conversations.replies` on a top-level message with no replies
  returns the parent alone — not an error, and the scan ignores it
  because it predates the mention.
- **Independent of `mergeDMOverride` / `RecentDMActivity`.** The fix
  lives in the mention-filter path; the DM-window unread sweep
  (ADR 014, v0.4.12) is untouched and still uses its own
  `users.conversations` + `dmHistorySince` pipeline.
- **Testability seam established.** Other helpers in
  `unread_helpers.go` that depend on `MessageClient` can follow the
  same shape if they grow tests later.

## Validation

- `go test -race -count=1 ./...` — green, 8 new tests added
  (`pending_reply_test.go`). Suite size 395 → 403.
- 8 new tests cover: top-level reply short-circuit (1 API call),
  thread-reply mention closed by in-thread answer, top-level mention
  closed by in-thread answer, no reply anywhere (true positive),
  older reply chronology guard, whitespace-only reply guard,
  history-error fallthrough to thread sweep, empty-selfID early
  exit.

## Out of scope

- A general "scan the whole channel for the operator's activity"
  fallback. Two sources (history + thread of relevant root) are
  enough; adding a third (workspace search, neighbouring threads)
  would inflate API cost for no observed gain.
- Surfacing the thread that closed the mention. Today the filter is
  binary (pending vs. not); rendering the closing message would
  require shipping a different output shape from `get_mentions` —
  separate change if needed.
- Cache for `ThreadReplies` results across mentions in the same
  thread. A multi-mention thread is unusual enough that the
  duplicate-call cost is acceptable; revisit only if production
  metrics show a hotspot.

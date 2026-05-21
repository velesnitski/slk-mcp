# ADR 007 — `with_thread_context` on `get_user_messages`

**Status:** accepted
**Date:** 2026-05-21
**Tag at acceptance:** v0.4.5

## Context

`get_user_messages` is the workspace-search wrapper used to answer
"what has user X been saying lately?" An LLM consumer running daily
sweeps hit a recurring blind spot: Slack's `search.messages` returns
the hit body and a permalink, but no thread-parent context. So a
polling loop would get rows like:

```
- #team-alpha 14:08 (alice) yep
- #team-alpha 14:29 (bob)   done
- #team-alpha 14:48 (bob)   not us
```

— hits that are *only* interpretable as replies to something. The
current workaround was to manually call `get_thread(permalink)` for
each ambiguous hit, which is two round-trips per fragment and easy
to forget when scanning a long sweep.

The signal isn't always there: top-level posts and thread parents
are self-explanatory. Only replies need the lift.

### Options considered

- **a.** Always inline parents. Doubles API cost on every
  `get_user_messages` call even when callers don't need it. Loud.
- **b.** Document the `get_thread` follow-up in the tool description
  and rely on the consumer to chain calls. Cheap, but consistent
  failure mode — easy to skip in a fast scan loop.
- **c.** Optional `with_thread_context: bool` parameter, default
  `false`. When set, identify replies via existing `ExtractThreadTS`
  helper, fetch each unique thread once with
  `conversations.replies`, and inline the parent on a continuation
  line. Cost is opt-in; non-breaking; matches the established
  pattern (`since`/`until`, `max_chars`, `skip_log_mode`).

## Decision

Use **(c)**. The contract change is:

1. New optional `with_thread_context: bool` (default `false`).
2. When `true`, the handler:
   - calls `format.ExtractThreadTS(m)` for each hit;
   - if the result differs from `m.Timestamp` *and* `m.Channel.ID`
     is non-empty, the hit is a reply that warrants enrichment;
   - dedups via `threadKey(m) = channelID|threadTS` so two replies
     in the same thread share one `conversations.replies` call;
   - emits one indented continuation line per hit, in the same
     monospace style as existing digest output (new
     `format.ThreadContextLine`).
3. `format.extractThreadTS` is renamed to **`ExtractThreadTS`**
   (exported) so the tools package can reuse the same parser
   without a copy. Internal call sites updated.

Best-effort fetch: a single failed `conversations.replies` logs at
debug and the line is silently dropped — never blocks the rendering
loop. The worst case is "missing parent line for that one hit," not
"failed `get_user_messages`."

## Consequences

- **Zero impact when off.** Default-false means existing callers see
  identical output.
- **Cost when on.** One `conversations.replies` call per unique
  thread among the hits — typically ≤ `limit` calls but often fewer
  because thread-bursts in real workspaces share parents. The dedup
  is what keeps this from being O(N) for noisy threads.
- **Slack scope.** The user-token (xoxp) used by handlers can read
  any channel the operator is a member of, including private ones.
  The new code does not change the scope — it just uses existing
  read permissions more thoroughly.
- **Surface area.** Two new exports in `format/`
  (`ExtractThreadTS`, `ThreadContextLine`). Both are pure functions
  with single tests in `format/thread_context_test.go`.
- **No new tool.** Resisted the urge to add `get_message_thread` or
  similar — keeping the surface flat is more important than a new
  tool name. The existing `get_thread` already covers the explicit
  drill-down use case.

## Validation

- `TestThreadKey_*` in `internal/tools/thread_context_test.go`:
  same-thread hits share a key (dedup works), cross-channel hits do
  not (channel-local timestamps aren't globally unique).
- `TestExtractThreadTS_*` pins the parser behaviour (toplevel falls
  back to `m.Timestamp`; permalink with `thread_ts=` is extracted).
- `TestThreadContextLine_*` in `internal/format/`: leading
  tab/marker, body truncation at 200 chars, fallback to user ID when
  display name unknown.
- Full suite: 341 tests green (334 → 341, +7).

## Out of scope

- Surrounding replies (1–2 messages before/after the hit in the same
  thread). Considered but deferred — the parent is the 80/20 win for
  fragmentary hits. If demand surfaces, add as `thread_context_window`
  later.
- Permalink-based variant for hits that lack `Channel.ID`. In
  practice Slack search results always populate the channel ID for
  visible channels; if a future workspace exposes a corner case,
  fall back to parsing the channel ID from the permalink.

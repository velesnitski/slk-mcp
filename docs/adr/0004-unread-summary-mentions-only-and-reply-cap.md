# ADR 0004: `mentions_only` filter and configurable reply cap

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0003 (thread context + mention markers).

## Context

After v0.2.5 shipped, the natural next ask was to be able to *only*
see channels where the operator is tagged — useful for a fast triage
pass before reading anything else. The thread-reply preview cap of 3
also turned out to be too low for some flows (long technical threads
lose context) and too high for others (drive-by stand-up replies just
add noise).

## Decision

Add two parameters to `get_unread_summary`:

- `mentions_only` (bool, default `false`) — when true, drop every
  channel that does not contain at least one direct `<@U_SELF>` in a
  top-level message or thread reply. The output header switches to
  `# Unread summary (mentions only)` so the consumer knows the
  filter was applied. If `auth.test` failed and we have no `selfID`,
  the tool returns an error instead of silently passing every
  channel through (would be misleading).
- `thread_preview_replies` (number, default `3`) — overrides the
  per-thread reply inline cap. Plumbed through as
  `format.WithThreadPreviewReplies(n)`. Values <= 0 fall back to the
  package default constant.

`filterMentions` lives in `internal/tools` (not the format package)
because it operates on `slack.ChannelUnread`, which is a slack-layer
type — keeping format pure of slack-specific structs.

## Consequences

- The `mentions_only` filter runs over already-fetched
  `ChannelUnread` results, so it does not save any Slack API calls;
  it only saves LLM tokens / human attention. That is the actual
  scarce resource here.
- `WithThreadPreviewReplies(0)` and negative values both fall back to
  the package default, so the existing `format.ThreadPreviewReplies`
  constant remains the single source of truth for the default.
- Test coverage added: `internal/tools/unread_helpers_test.go` —
  16 cases covering `channelMentions` (top-level vs reply, empty
  selfID), `filterMentions` (kept/dropped, empty selfID no-op),
  `rankUnread` (mentions outrank volume, ties broken by volume,
  replies count toward volume), and `collectUserIDsWithReplies`
  (dedupes across messages and replies, skips empty user). Plus
  `format.ChannelDigest` cap-override cases.

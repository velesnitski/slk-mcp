# Architecture Decision Records

Short, point-in-time notes on non-obvious decisions. The goal is to
spare a future maintainer (often: future me) from re-litigating a
choice that already had a careful answer.

## When to write one

- The decision is **non-obvious** (the obvious path is the wrong one,
  or two paths are equally defensible and we picked one for stated
  reasons).
- The decision is **load-bearing** (other code or future decisions
  rest on this one being true).
- We made the decision **with context that will fade** (a Slack API
  quirk, a downstream-tool quirk, a one-time incident, a perf budget
  we measured but didn't enshrine in a test).

Skip ADRs for changes that are self-explanatory from the diff, the
commit message, or the CHANGELOG.

## Format

Number sequentially: `001-short-slug.md`, `002-…`. Each ADR has:

```markdown
# ADR N — Title

**Status:** accepted | superseded by ADR M | reverted
**Date:** YYYY-MM-DD
**Tag at acceptance:** vX.Y.Z

## Context

What problem we were solving. Include the option(s) we rejected and
why; this is the most valuable section for future readers.

## Decision

The choice we made, stated as a rule.

## Consequences

What this commits us to. What it forecloses.
```

Keep each ADR under one page when possible. Edit later if reality
diverges from the prediction — note the divergence at the bottom,
don't rewrite history.

## Index

- [001 — GIT MODE prefers MR-iid over issue-id](001-mr-iid-priority.md)
- [002 — Unified `id→name` refs map for users and channels](002-unified-refs-map.md)
- [003 — `Hub` receiver replaces `Deps` service-locator](003-hub-receiver.md)
- [004 — Absolute-time `since`/`until` on `get_user_messages`](004-absolute-time-user-search.md)
- [005 — Handler migration to `Hub.X()` accessors + table-driven registration](005-table-driven-registration.md)
- [006 — `get_unread_summary` size controls (`max_chars`, `skip_log_mode`, `skip_git_mode`)](006-unread-summary-size-controls.md)
- [007 — `with_thread_context` on `get_user_messages`](007-with-thread-context.md)
- [008 — Permalink-ID short-circuit + hidden-payload markers](008-permalink-id-shortcircuit-and-payload-markers.md)
- [009 — DM time-window override on `get_unread_summary`](009-dm-window-on-unread-summary.md)
- [010 — Thread-mention backstop on `get_unread_summary`](010-thread-mention-backstop.md)
- [011 — Channel audit on `list_channels`](011-list-channels-audit.md)
- [012 — `parent_test.go` deadline raised to 5s under `-race`](012-parent-test-deadline.md)
- [013 — `archive_channel` and `unarchive_channel`](013-archive-channel.md)
- [014 — DM-window silent-miss bug](014-dm-window-silent-miss.md)
- [015 — `parent_test.go` poll interval raised from 1ms to 10ms](015-parent-test-poll-interval.md)
- [016 — `pending_only` mention filter must also check thread replies](016-pending-mentions-thread-replies.md)
- [017 — Self-reported MCP server name embeds the version](017-server-name-embeds-version.md)
- [018 — `get_list_items` for Slack Lists via raw HTTP](018-slack-lists-tool.md)

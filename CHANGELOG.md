# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.5] - 2026-04-30

### Added
- `list_users` tool — enumerate active workspace users with handle, real name, admin/owner/guest/bot flags. `include_bots` (bool, default false) opt-in for bot/integration accounts. Useful for auditing handle conventions and onboarding gaps.

## [0.3.4] - 2026-04-30

### Changed
- User names in digests render as `"Real Name (handle)"` so the LLM can correlate Slack handles with the human behind them. Falls through to whichever field is available; no parens when the two would be identical.

## [0.3.3] - 2026-04-30

### Fixed
- `Unread()` no longer short-circuits on `unread_count == 0`. Slack reports zero for muted and high-traffic channels even when messages newer than `last_read` exist; the digest now drives off `last_read` alone, surfacing those channels.

## [0.3.2] - 2026-04-30

### Fixed
- **`get_unread_summary` now covers direct messages and group DMs.** Previously, `JoinedChannels` only requested `public_channel` and `private_channel` types from `users.conversations`, silently dropping every DM. Operators saw "all caught up — 0 unread" while `get_mentions` showed dozens of hits in DMs. Types list now includes `im` and `mpim`. See `docs/adr/0009-include-direct-messages-in-unread-sweep.md`.

### Changed
- **Digest headers are now caller-prefixed.** `format.ChannelDigest` and `format.LogChannelDigest` previously hardcoded a `#` prefix in the heading (`## #channelname`). They now take a verbatim `channelLabel` so callers can pick the right prefix per channel kind: `#` for channels, `@peer` for IMs, `mpdm-...` for group DMs. New helper `tools.channelDisplayLabel(ctx, ch, users)` does the routing. LLM consumers that pattern-match on `## #` should relax to `## ` followed by the label.
- README and docker-compose example `SLACK_CHANNELS` switched to generic placeholders.

### Added
- 6 new unit tests in `internal/tools/unread_dm_test.go` covering each branch of `channelDisplayLabel` (regular channel, empty-name channel, mpim with name, mpim without name, im without user, im with user).

## [0.3.1] - 2026-04-30

### Changed
- **Log-mode rendering now dedupes near-identical messages.** A new `canonicalSignature` pass replaces URLs, IPv4 addresses, hex IDs (≥7 chars), and digit runs with placeholders, lowercases, and collapses whitespace. Messages sharing a signature group into a single `LogPattern` rendered as `"[hh:mm bot] body (×N similar)"`. `samplesPerBand` parameter now caps distinct patterns per band (still default 3); same name, sharper semantics. See `docs/adr/0008-log-pattern-dedup.md`.

### Added
- `format.LogPattern{Sample, Count, Signature}` — public type used by `LogBand.Patterns`.
- `LogBand.Patterns []LogPattern` — preferred field; renderer prefers it over the legacy `Samples` path. Existing callers that populate `Samples` keep getting per-message rendering (backwards compatible).
- 33 new unit tests in `internal/tools/dedup_test.go` covering each regex (URL > IP > hex > digit ordering), family-merge invariants, recency tiebreak in pattern sort, top-N + remainder math, and the renderer's pattern + legacy-fallback paths.

### Notes
- Conservative dedup: alerts that differ by alphabetic detail stay distinct (e.g. `"high cpu on dc1"` vs `"high cpu on dc-1"` — hyphen alone won't merge them). This is intentional — over-merging genuinely different incidents costs more than rendering two near-duplicate lines.

## [0.3.0] - 2026-04-30

### Added
- **Log-channel mode in `get_unread_summary`.** Bot-driven channels (monitoring, ci, registry, cloud, etc.) are auto-detected and rendered as a severity histogram (`FATAL=2 ERROR=12 WARN=3 INFO=8`) followed by sample messages per band, instead of the per-message digest used for human conversations. Saves ~70% of the tokens these channels used to consume. See `docs/adr/0007-log-channel-mode.md`.
- **Auto-detection heuristic** — channels are classified as logs when ≥50% of unread messages are bot-authored (`bot_id` set or `bot_message` subtype) OR when the channel name contains `log`, `alert`, `alarm`, `monitor`, `monitoring`, `metric`/`metrics`, `report`/`reports`, `cron`, or `incident`. Name fallback catches webhook-style integrations that post under real user accounts.
- **`log_mode` parameter** (`auto` | `off`, default `auto`) — escape hatch when auto-detection misclassifies.
- **`log_samples_per_band` parameter** (number, default `3`) — cap on the "recent X" sample list per severity band.
- New types: `format.LogBand`, `format.LogChannelDigest`, `tools.LogSeverity` with five bands (FATAL > ERROR > ALERT > WARN > INFO).

### Notes
- Log mode does NOT inline thread replies or mention markers in the rendered output. Bot channels rarely thread, and humans following up on an alert are low-volume; if needed, drop to `log_mode=off` for the full digest.
- Severity classification reuses the same English log-vocabulary as the v0.2.8 urgency keyword block, so a message that bumps urgency for channel ranking will also classify into the matching band.

## [0.2.8] - 2026-04-30

### Added
- **`urgency_weight` parameter on `get_unread_summary`** — multiplier on the urgency score before ranking (default `1.0`). Zero or negative values fall back to the default; pass `0.5` to dampen, `2.0` to amplify. See `docs/adr/0006-urgency-tuning-and-log-channel-keywords.md`.
- **`urgency_keywords` parameter on `get_unread_summary`** — comma-separated extra keywords additive to the built-in en/ru list. Useful for domain-specific terms like `"p0, prod down, internal-tool"` without redeploying.
- **English log-severity keywords in the built-in list** — `error`, `errors`, `failed`, `failure`, `fatal`, `alert`, `exception`, `panic`, `outage`, `timed out`, plus Russian `не отвечает`. Bot-driven channels (zabbix / gitlab / harbor / aws) now surface real failures above routine info without configuration.

### Notes
- Deliberately omitted from the built-in list: `down` (matches `downloaded`/`markdown`/`cooldown` — too noisy) and `fail` (superset of `failed` and `failure`, would double-score). Both can still be added via `urgency_keywords` on a per-call basis.

## [0.2.7] - 2026-04-30

### Changed
- **`get_unread_summary` now ranks channels by urgency, not just volume.** A new heuristic in `internal/tools/urgency.go` scores each unread channel from per-message signals: question marks (capped at 3 per message), urgency keywords in English and Russian (`urgent`/`срочно`/`сломалось`/...), urgency-suggesting reactions (`rotating_light`, `fire`, `warning`, ...), and recency (`<1h` and `<6h` bands). A single keyword outranks ~9 plain messages; mentions of the operator still dominate any non-mention channel. See `docs/adr/0005-urgency-heuristic-for-unread-ranking.md`.

### Added
- `internal/tools/urgency.go` + `internal/tools/urgency_test.go` (14 cases): per-signal tests, recency bands, ranking-interaction invariants (mention > urgency > volume), full-width `？` handling, keyword case-insensitivity in Cyrillic.

## [0.2.6] - 2026-04-30

### Added
- **`mentions_only` parameter on `get_unread_summary`** — when true, returns only channels containing at least one direct `<@U_OPERATOR>` mention (top-level or in a thread reply). Header switches to `# Unread summary (mentions only)` so callers can distinguish. See `docs/adr/0004-unread-summary-mentions-only-and-reply-cap.md`.
- **`thread_preview_replies` parameter on `get_unread_summary`** — overrides the per-thread inline reply cap (default 3). Plumbed through as `format.WithThreadPreviewReplies(n)`; non-positive values fall back to `format.ThreadPreviewReplies`.

### Changed
- Tool helpers in `internal/tools/unread.go` (`channelMentions`, `filterMentions`, `rankUnread`, `collectUserIDsWithReplies`) are now covered by `internal/tools/unread_helpers_test.go` — 16 new cases.

## [0.2.5] - 2026-04-30

### Added
- **Thread context in `get_unread_summary`.** Top-level unread messages that are thread parents now have their post-`last_read` replies fetched and rendered indented (`↳ ...`) under the parent. Capped at 3 replies per thread with a `+N more replies` collapse for the rest. See `docs/adr/0003-unread-summary-thread-context-and-mention-marker.md`.
- **Mention markers (`🏷️`) in the unread digest.** Messages whose body contains `<@U_OPERATOR>` are prefixed with a marker character so the LLM (and the human) can spot direct asks at a glance. The operator's user ID is resolved once via `auth.test` and cached.
- **Mention-aware channel ranking.** `get_unread_summary` now sorts channels with at least one direct mention ahead of busier-but-impersonal channels.
- `UnreadService.Self(ctx)` — cached self-user resolution for tools that need to know who "you" are (Slack-side).
- `format.WithMentionHighlight` / `format.WithThreadReplies` — variadic `DigestOption` API; existing `ChannelDigest` callers unchanged.

### Limits
- Replies on threads whose parent is *already* read are not surfaced by `get_unread_summary` (would require a per-channel `latest_reply > last_read` scan). Use `get_mentions` for that case — it hits `search.messages` and catches the mention regardless of thread state.

## [0.2.4] - 2026-04-30

### Fixed
- **stdio transport now exits when its parent MCP host process dies.** Previously, hosts that disconnected without closing stdin (e.g. some Claude Code reconnect paths) left orphan `slk-mcp` processes around, requiring manual `pkill`. The new `internal/lifecycle.WatchParent` polls `os.Getppid()` and exits when it changes (parent reparented to PID 1 / launchd). See `docs/adr/0002-parent-pid-watcher-for-stdio-orphans.md`.

### Added
- `internal/lifecycle` package with `WatchParent` plus 6 unit tests covering ppid-change detection, single-shot semantics, context-cancel exit, nil-logger safety, and zero-interval default behaviour.

## [0.2.3] - 2026-04-30

### Fixed
- **`get_unread_summary` no longer reports "0 unread" when channels have new messages.** `UnreadAll` was filtering channels using `unread_count` from `users.conversations`, which Slack does not populate on that endpoint. Filter is now driven by the per-channel `conversations.info` lookup (which already short-circuits when caught up). See `docs/adr/0001-unread-summary-trusts-conversations-info.md`.

### Added
- Unit tests for the unread service (`internal/slack/unread_test.go`) covering the regression, token gating, last-read boundary handling, pagination, and `mark_read`.

## [0.2.2] - 2026-04-30

### Added
- **Channel auto-discovery** — when no `channels` argument is passed and `SLACK_CHANNELS` is empty, the digest, recap, and decision tools fall back to every channel the active identity has joined (user token: `users.conversations`; bot token: bot's joined channels).
- New env var `SLACK_AUTODISCOVER_LIMIT` (default `50`) caps the auto-discovered list.
- `Client.JoinedChannelNames(ctx, limit)` — single entry point for the active identity's channels, sorted by member count, archived filtered.

### Changed
- `parseChannelList` no longer takes a defaults argument; channel resolution is now centralised in `resolveTargetChannels` (input → config → auto).
- Tools log the resolved channel count when auto-discovery runs.

## [0.2.1] - 2026-04-14

### Changed
- **Token model is now flexible** — at least one of `SLACK_TOKEN` (`xoxb-`) or `SLACK_USER_TOKEN` (`xoxp-`) is required, not both. A user-only setup is now fully supported and acts as the authenticated user for all API calls (posts appear under the user's name).
- `slack.Client` picks the primary API from `Config.PrimaryToken()`; when the user token is primary, the bot HTTP client pool is not allocated.
- Startup log now reports the active token mode (`bot-only`, `user-only`, `bot + user`).
- `ErrMissingBotToken` → `ErrMissingToken`.
- README: split "Create a Slack App" into user-only (Setup A) and bot (Setup B) recipes.

## [0.2.0] - 2026-04-14

### Added
- **`get_unread_summary`** — smart summary of every unread message across joined channels, grouped per channel. Requires `SLACK_USER_TOKEN`.
- **`get_mentions`** — direct mentions of the authenticated user over a time window.
- **`mark_read`** — mark a channel as read up to a given message timestamp.
- **User token support** (`SLACK_USER_TOKEN`, `xoxp-...`) alongside the bot token. Gates unread/mentions/mark_read tools.
- **Rate-limit handling** — `internal/slack/ratelimit` retries on `429` with the `Retry-After` value Slack returns, up to 5 attempts.
- **Context propagation** — every Slack API call uses the `*Context` variant, so tool cancellation and timeouts flow through.
- **Compact output formatter** (`SLACK_COMPACT=true` by default) — single-line messages with truncation markers (`+127 chars`) and per-channel caps (`+N more messages`) to reduce LLM token consumption.
- **Structured logging** with `log/slog` (JSON, stderr). Configurable via `-log-level` or `SLACK_LOG_LEVEL`.
- **Graceful shutdown** on SIGINT/SIGTERM with a 10 s timeout for HTTP transports.
- `-version` flag.
- Unit tests for config parsing and formatters.

### Changed
- **Architecture** — `slack.Client` now composes narrow services: `Channels`, `Messages`, `Users`, `Search`, `Unread`. Tool handlers depend on services they actually use.
- **User name resolution** — cached behind a `sync.RWMutex` and batched via `UserService.NamesFor`.
- **Search** now prefers the user token when configured (`search.messages` is gated on user tokens for newer Slack apps).
- Tool handlers now consistently return `NewToolResultError` for user-facing errors and use `errors.Is` / wrapped errors internally.
- `DISABLED_TOOLS` is checked per-tool at registration time instead of post-hoc in the `_tool_manager` map.

### Fixed
- Concurrent access to the channel/user caches is now safe.
- Whitespace collapsing in message bodies (multi-line messages no longer break single-line output).

## [0.1.0] - 2026-04-14

### Added
- Initial release
- 10 tools: list_channels, get_channel_info, get_channel_digest, get_multi_channel_digest, get_morning_recap, search_messages, find_decisions, get_thread, get_user_messages, post_message, add_reaction
- Morning recap with decision detection (keywords + reactions)
- Multi-channel digest in a single call
- Slack search syntax support (from:@user, in:#channel, has:link)
- Read-only mode (`SLACK_READ_ONLY=true`)
- Tool filtering via `DISABLED_TOOLS`
- Default channels via `SLACK_CHANNELS` env var
- Docker support (Alpine-based, ~15 MB image)
- SSE and streamable-http transports

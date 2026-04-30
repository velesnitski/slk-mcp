# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

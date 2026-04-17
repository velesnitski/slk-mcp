# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

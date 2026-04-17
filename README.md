# slk-mcp

Slack MCP server for [Claude Code](https://claude.com/claude-code), [GitHub Copilot](https://github.com/features/copilot), [Cursor](https://cursor.com), [JetBrains IDEs](https://www.jetbrains.com/help/idea/mcp.html), and any MCP-compatible client.

**What it does:** morning recaps across channels, smart unread summaries, mentions tracking, decision detection, message search, thread reading, and posting.

**Written in Go** — single ~10 MB binary, ~15 MB Docker image, no runtime, no zombie processes.

## Quick start

### 1. Create a Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**
2. Under **OAuth & Permissions**, add scopes:
   - **Bot Token Scopes** (required):
     - `channels:history`, `channels:read`
     - `groups:history`, `groups:read`
     - `users:read`
     - `chat:write` (optional — for `post_message`)
     - `reactions:write` (optional — for `add_reaction`)
   - **User Token Scopes** (optional, unlocks unread/mentions):
     - `channels:history`, `groups:history`, `im:history`, `mpim:history`
     - `users.profile:read`
     - `search:read`
3. **Install to Workspace**, then copy:
   - `xoxb-...` — Bot User OAuth Token → `SLACK_TOKEN`
   - `xoxp-...` — User OAuth Token → `SLACK_USER_TOKEN` (optional)
4. Invite the bot to the channels you care about: `/invite @your-bot`

### 2. Install in Claude Code

```bash
claude mcp add slack \
  -e SLACK_TOKEN=xoxb-your-bot-token \
  -e SLACK_USER_TOKEN=xoxp-your-user-token \
  -e SLACK_CHANNELS=general,announcements \
  -- docker run --rm -i \
     -e SLACK_TOKEN -e SLACK_USER_TOKEN -e SLACK_CHANNELS \
     velesnitski/slk-mcp -transport stdio
```

Or via config file (`~/.claude.json`):

```json
{
  "mcpServers": {
    "slack": {
      "command": "docker",
      "args": ["run", "--rm", "-i",
               "-e", "SLACK_TOKEN",
               "-e", "SLACK_USER_TOKEN",
               "-e", "SLACK_CHANNELS",
               "velesnitski/slk-mcp",
               "-transport", "stdio"],
      "env": {
        "SLACK_TOKEN": "xoxb-your-bot-token",
        "SLACK_USER_TOKEN": "xoxp-your-user-token",
        "SLACK_CHANNELS": "general,announcements"
      }
    }
  }
}
```

### 3. Try it

- *"Summarise my unread messages"*
- *"What happened in #general overnight?"*
- *"Morning recap"*
- *"Any mentions of me in the last 24 hours?"*
- *"What decisions were made this week across my channels?"*
- *"Search for messages about database migration"*
- *"What did alex say about the deployment?"*

## Tools

### Without user token (bot-token only)

| Tool | Description |
|---|---|
| `list_channels` | List channels the bot can see, ordered by member count |
| `get_channel_info` | Topic, purpose, member count, created date |
| `get_channel_digest` | Compact digest of one channel |
| `get_multi_channel_digest` | Digest across multiple channels |
| `get_morning_recap` | Decisions + activity across channels |
| `search_messages` | Workspace search (Slack syntax) |
| `find_decisions` | Messages that look like decisions (keywords + reactions) |
| `get_thread` | Full thread replies |
| `get_user_messages` | Recent messages from a specific user |
| `post_message` | Post a message (disabled in `SLACK_READ_ONLY`) |
| `add_reaction` | Add an emoji reaction (disabled in `SLACK_READ_ONLY`) |

### With user token (`SLACK_USER_TOKEN`)

| Tool | Description |
|---|---|
| `get_unread_summary` | Unread messages across all joined channels, sorted by volume |
| `get_mentions` | Messages that mention you |
| `mark_read` | Mark a channel read through a given timestamp |

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `SLACK_TOKEN` | Yes | Bot User OAuth Token (`xoxb-...`) |
| `SLACK_USER_TOKEN` | No | User OAuth Token (`xoxp-...`). Enables unread/mentions. |
| `SLACK_CHANNELS` | No | Default channels for digest/recap (comma-separated) |
| `SLACK_READ_ONLY` | No | `true` to disable `post_message`, `add_reaction`, `mark_read` |
| `SLACK_DIGEST_HOURS` | No | Default digest lookback (default: `24`) |
| `SLACK_MAX_MESSAGES` | No | Cap on messages fetched per channel per call (default: `200`) |
| `SLACK_COMPACT` | No | `false` to disable compact output (default: `true`) |
| `SLACK_LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |
| `DISABLED_TOOLS` | No | Comma-separated list of tool names to hide |

## Docker

```bash
docker run -d --name slack-mcp \
  -e SLACK_TOKEN=xoxb-your-bot-token \
  -e SLACK_USER_TOKEN=xoxp-your-user-token \
  -e SLACK_CHANNELS=general,announcements \
  -p 8001:8000 \
  velesnitski/slk-mcp
```

Connect Claude Code over the network:

```json
{
  "mcpServers": {
    "slack": {
      "type": "url",
      "url": "http://localhost:8001/sse"
    }
  }
}
```

## Architecture

```
slk-mcp/
├── main.go                          # Entry, flags, transport, graceful shutdown
├── internal/
│   ├── config/                      # Env-driven configuration + validation
│   ├── logger/                      # slog JSON logger (stderr)
│   ├── format/                      # Compact LLM-friendly output
│   ├── slack/
│   │   ├── client.go                # Composition root
│   │   ├── channels.go              # ChannelService — list, resolve, info
│   │   ├── messages.go              # MessageService — history, threads, post
│   │   ├── users.go                 # UserService — cached name resolution
│   │   ├── search.go                # SearchService — workspace search
│   │   ├── unread.go                # UnreadService — user-token-only
│   │   └── ratelimit/               # 429 retry with Retry-After
│   └── tools/                       # MCP tool handlers (one file per domain)
└── Dockerfile                       # Multi-stage, non-root, Alpine
```

Services are composed on `slack.Client`; each tool handler depends only on what it needs. Rate-limiting wraps every API call. `context.Context` propagates from tool handlers to the Slack SDK.

## Decision detection

`find_decisions` and `get_morning_recap` detect decisions by:

**Keywords in text:** "decided", "approved", "let's go with", "agreed", "confirmed", "moving forward", "final answer"

**Reactions on messages:** :white_check_mark:, :heavy_check_mark:, :eyes:, :thumbsup:

## Security

- Tokens passed via environment variables — never hardcoded.
- Stdio transport has zero network exposure.
- Read-only mode available (`SLACK_READ_ONLY=true`).
- Bot only sees channels it's invited to.
- User token only needed for personal workflow features (unread/mentions) — bot token covers everything else.
- No message content is logged, only tool names, channel names, and timings.

## Development

```bash
go build -o slk-mcp .
go test ./...
go vet ./...
```

## License

MIT

# slk-mcp

Slack MCP server for [Claude Code](https://claude.com/claude-code), [GitHub Copilot](https://github.com/features/copilot), [Cursor](https://cursor.com), [JetBrains IDEs](https://www.jetbrains.com/help/idea/mcp.html), and any MCP-compatible client. Get morning recaps, search messages, and track decisions across channels.

## Quick start

### 1. Create a Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**
2. Add **Bot Token Scopes** under **OAuth & Permissions**:
   - `channels:history` — read public channel messages
   - `channels:read` — list channels
   - `groups:history` — read private channel messages
   - `groups:read` — list private channels
   - `users:read` — resolve user names
   - `search:read` — search messages
   - `chat:write` — post messages (optional)
   - `reactions:write` — add reactions (optional)
3. **Install to Workspace** and copy the **Bot User OAuth Token** (`xoxb-...`)
4. Invite the bot to channels: `/invite @your-bot-name`

### 2. Install in Claude Code

```bash
claude mcp add slack \
  -e SLACK_TOKEN=xoxb-your-bot-token \
  -e SLACK_CHANNELS=general,announcements \
  -- uvx --from git+https://github.com/velesnitski/slk-mcp slk-mcp
```

Or manually in `~/.claude.json`:

```json
{
  "mcpServers": {
    "slack": {
      "command": "uvx",
      "args": ["--from", "git+https://github.com/velesnitski/slk-mcp", "slk-mcp"],
      "env": {
        "SLACK_TOKEN": "xoxb-your-bot-token",
        "SLACK_CHANNELS": "general,announcements"
      }
    }
  }
}
```

### 3. Try it

- *"Give me a morning recap"*
- *"What happened in #devops in the last 24 hours?"*
- *"What decisions were made this week?"*
- *"What did John say about the deployment?"*
- *"Search for messages about database migration"*

## Available tools (10)

### Channels (2)

| Tool | Description |
|---|---|
| `list_channels` | List accessible channels with member counts and topics |
| `get_channel_info` | Detailed info about a channel |

### Digest & Recap (3)

| Tool | Description |
|---|---|
| `get_channel_digest` | Recent messages from a single channel |
| `get_multi_channel_digest` | Digest across multiple channels in one call |
| `get_morning_recap` | Morning recap: digest + decisions + action items |

### Search (2)

| Tool | Description |
|---|---|
| `search_messages` | Search messages across all channels (Slack search syntax) |
| `find_decisions` | Find messages that look like decisions (keywords + reactions) |

### Threads & Messages (3)

| Tool | Description |
|---|---|
| `get_thread` | Get all replies in a thread |
| `get_user_messages` | Get recent messages from a specific user |
| `post_message` | Post a message to a channel |
| `add_reaction` | Add a reaction to a message |

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `SLACK_TOKEN` | Yes | Bot User OAuth Token (`xoxb-...`) |
| `SLACK_CHANNELS` | No | Default channels for digest/recap (comma-separated) |
| `SLACK_READ_ONLY` | No | Set to `true` to disable post_message and add_reaction |
| `SLACK_DIGEST_HOURS` | No | Default digest lookback (default: `24`) |
| `DISABLED_TOOLS` | No | Comma-separated list of tools to disable |
| `SENTRY_DSN` | No | Sentry DSN for error tracking |
| `SLACK_LOG_FILE` | No | Error log path (default: `~/.slk-mcp/slk-mcp.log`) |
| `SLACK_ANALYTICS_FILE` | No | Analytics log path (default: `~/.slk-mcp/analytics.log`) |

## Docker

```bash
docker run -d --name slack-mcp \
  -e SLACK_TOKEN=xoxb-your-bot-token \
  -e SLACK_CHANNELS=general,announcements \
  -p 8001:8000 \
  velesnitski/slk-mcp
```

Connect Claude Code to Docker:

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

## Decision detection

The `find_decisions` and `get_morning_recap` tools detect decisions by:

**Keywords** in message text:
- "decided", "approved", "let's go with", "agreed", "confirmed", "moving forward", "final answer"

**Reactions** on messages:
- :white_check_mark:, :heavy_check_mark:, :eyes:, :thumbsup:

## Security

- Bot token passed via environment variable — never hardcoded
- In stdio mode, no network exposure (local pipes only)
- Read-only mode available (`SLACK_READ_ONLY=true`)
- The bot can only see channels it's been invited to
- No message content is logged — only tool names, channel names, and timing

## License

MIT

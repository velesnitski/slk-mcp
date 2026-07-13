# slk-mcp

[![Tests](https://github.com/velesnitski/slk-mcp/actions/workflows/test.yml/badge.svg)](https://github.com/velesnitski/slk-mcp/actions/workflows/test.yml)
[![Go](https://img.shields.io/badge/go-1.23%2B-00ADD8.svg?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/velesnitski/slk-mcp)](https://github.com/velesnitski/slk-mcp/releases)
[![Tools](https://img.shields.io/badge/tools-22-purple.svg)](#tools)
[![Stars](https://img.shields.io/github/stars/velesnitski/slk-mcp?style=social)](https://github.com/velesnitski/slk-mcp/stargazers)

Slack MCP server for [Claude Code](https://claude.com/claude-code), [GitHub Copilot](https://github.com/features/copilot), [Cursor](https://cursor.com), [JetBrains IDEs](https://www.jetbrains.com/help/idea/mcp.html), and any MCP-compatible client.

**What it does:** morning recaps across channels, smart unread summaries, mentions tracking, decision detection, message search, thread reading, and posting.

**Written in Go** — single ~10 MB binary, ~15 MB Docker image, no runtime, no zombie processes.

## Quick start

### 1. Create a Slack App

Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**.
Pick **one** of the setups below depending on how you want slk-mcp to act.

#### Setup A — user token only (recommended for personal use)

Use this if you want slk-mcp to act as **you**: posts appear under your name,
DMs and private channels work out of the box, full unread/mentions support.
You don't need to invite anything to channels.

Under **OAuth & Permissions → User Token Scopes**, add:

- `channels:history`, `channels:read`
- `groups:history`, `groups:read`
- `im:history`, `im:read`, `mpim:history`, `mpim:read`
- `users:read`
- `search:read`
- `chat:write` (optional — for `post_message`)
- `reactions:write` (optional — for `add_reaction`)

**Install to Workspace**, copy the **User OAuth Token** (`xoxp-...`) → `SLACK_USER_TOKEN`.

#### Setup B — bot token (shared team use)

Use this if you want slk-mcp to act as a **bot**: posts appear as the bot,
the bot must be invited to channels. Unread/mentions tools stay hidden
unless you also add a user token.

Under **OAuth & Permissions → Bot Token Scopes**, add:

- `channels:history`, `channels:read`
- `groups:history`, `groups:read`
- `users:read`
- `chat:write` (optional — for `post_message`)
- `reactions:write` (optional — for `add_reaction`)

Optional — add **User Token Scopes** to unlock `get_unread_summary`, `get_mentions`, `mark_read`:

- `channels:history`, `groups:history`, `im:history`, `mpim:history`
- `users.profile:read`, `search:read`

**Install to Workspace**, copy:

- `xoxb-...` — Bot User OAuth Token → `SLACK_TOKEN`
- `xoxp-...` — User OAuth Token → `SLACK_USER_TOKEN` (optional)

Invite the bot to channels: `/invite @your-bot`.

### 2. Install in Claude Code

**Setup A (user token only — recommended):**

```bash
claude mcp add slack \
  -e SLACK_USER_TOKEN=xoxp-your-user-token \
  -- docker run --rm -i \
     -e SLACK_USER_TOKEN \
     velesnitski/slk-mcp -transport stdio
```

Or via config file (`~/.claude.json`):

```json
{
  "mcpServers": {
    "slack": {
      "command": "docker",
      "args": ["run", "--rm", "-i",
               "-e", "SLACK_USER_TOKEN",
               "velesnitski/slk-mcp",
               "-transport", "stdio"],
      "env": {
        "SLACK_USER_TOKEN": "xoxp-your-user-token"
      }
    }
  }
}
```

**Setup B (bot token):** add `-e SLACK_TOKEN=xoxb-...` instead of (or alongside) the user token. Bot must be invited to each channel; unread/mentions/DM tools stay hidden unless a user token is also set.

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
| `delete_message` | Delete a message by permalink or channel+ts — only ones this token posted (disabled in `SLACK_READ_ONLY`) |
| `set_status` | Set/clear your custom status (text + emoji + auto-expiry) and optional presence; you-global by default (disabled in `SLACK_READ_ONLY`, needs user token) |
| `set_presence` | Flip presence away/auto WITHOUT touching your status; you-global by default (disabled in `SLACK_READ_ONLY`, needs user token) |
| `download_audio` | Download audio attachments (voice messages) to local temp files for transcription |
| `view_image` | Fetch image attachments (screenshots, photos, cards) and return them inline so the model can see them |
| `transcribe_audio` | Voice message, audio/video clip, or recorded huddle → text via a local whisper.cpp install; degrades to `download_audio` behaviour when the toolchain is missing |
| `analyze_audio_tone` | Vocal tone of a voice message — loudness range (LRA) + native pitch (f0 mean/variability) — to gauge calm vs agitated/shouting; needs only ffmpeg |

### With user token (`SLACK_USER_TOKEN`)

| Tool | Description |
|---|---|
| `get_unread_summary` | Unread messages across all joined channels, sorted by volume |
| `get_mentions` | Messages that mention you |
| `mark_read` | Mark a channel read through a given timestamp |

## How-to: transcribe voice messages

Slack voice notes are `audio/mp4` (.m4a) attachments, fetched with the
server's own token (the token never leaves the server process; requires
the **`files:read`** scope, or Slack answers with its sign-in page and
the tool reports a scope error).

**One call** — `transcribe_audio` runs the whole pipeline under the
hood when [whisper.cpp](https://github.com/ggerganov/whisper.cpp) is
installed:

```bash
# one-time setup (same as below); then:
# transcribe_audio { "permalink": "https://<workspace>.slack.com/archives/D.../p...", "language": "ru" }
# → transcript text. Missing toolchain? The tool falls back to
#   download_audio behaviour: local file path + a setup hint.
#
# No permalink and no ts? Point at the conversation and get its newest
# voice note (latest-mode) — ideal for empty-text memos search can't find:
# transcribe_audio { "channel": "@teammate", "from": "me", "language": "ru" }
#   channel: a DM as @handle, or a #name / C·G·D id. from: @handle or "me".
```

Optional overrides: `SLACK_FFMPEG_BIN`, `SLACK_WHISPER_BIN` (defaults:
PATH lookup of `ffmpeg` / `whisper-cli`) and `SLACK_WHISPER_MODEL`
(default: `~/.cache/whisper/ggml-small.bin`).

**Manual** — `download_audio` just fetches the file; pair it with any
local speech-to-text engine:

```bash
# one-time setup
brew install whisper-cpp ffmpeg
mkdir -p ~/.cache/whisper
curl -L -o ~/.cache/whisper/ggml-small.bin \
  "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
```

```bash
# 1. download the voice note (MCP call — returns a local temp path)
#    download_audio { "permalink": "https://<workspace>.slack.com/archives/D.../p..." }

# 2. convert to 16 kHz mono WAV and transcribe (pick your language via -l)
ffmpeg -y -i /tmp/slk-audio-<id>-<name>.m4a -ar 16000 -ac 1 /tmp/voice.wav
whisper-cli -m ~/.cache/whisper/ggml-small.bin -l ru -np /tmp/voice.wav
```

Model sizes trade accuracy for speed: `ggml-base.bin` (142 MB, fast),
`ggml-small.bin` (466 MB, good multilingual balance), `ggml-medium.bin`
(1.5 GB), `ggml-large-v3.bin` (3 GB, best). For typical voice notes,
`small` is enough. An MCP client (e.g. Claude Code) can run the whole
chain from a single message permalink.

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `SLACK_TOKEN` | one of | Bot User OAuth Token (`xoxb-...`). |
| `SLACK_USER_TOKEN` | one of | User OAuth Token (`xoxp-...`). Required for unread/mentions. At least one of `SLACK_TOKEN` / `SLACK_USER_TOKEN` must be set. |
| `SLACK_WORKSPACE_NAME` | No | Cosmetic label for the primary workspace, shown in merged digests (default: `primary`). |
| `SLACK_WORKSPACES` | No | JSON array of *additional* workspaces. Each entry: `{"name","bot_token","user_token","channels"}` (all but at least one token optional). Tokens are workspace-scoped, so each extra Slack space needs its own token pair. Labels live in the JSON values — no per-workspace env keys. See [Multiple workspaces](#multiple-workspaces). |
| `SLACK_CHANNELS` | No | Default channels for digest/recap (comma-separated). If unset, tools auto-discover the channels you've joined. |
| `SLACK_AUTODISCOVER_LIMIT` | No | Cap on auto-discovered channel count when `SLACK_CHANNELS` is unset (default: `50`) |
| `SLACK_READ_ONLY` | No | `true` to disable `post_message`, `add_reaction`, `delete_message`, `mark_read` |
| `SLACK_FFMPEG_BIN` | No | ffmpeg binary for `transcribe_audio` (default: `ffmpeg` from PATH) |
| `SLACK_WHISPER_BIN` | No | whisper.cpp binary for `transcribe_audio` (default: `whisper-cli` from PATH) |
| `SLACK_WHISPER_MODEL` | No | ggml model file for `transcribe_audio` (default: `~/.cache/whisper/ggml-small.bin`) |
| `SLACK_DIGEST_HOURS` | No | Default digest lookback (default: `24`) |
| `SLACK_MAX_MESSAGES` | No | Cap on messages fetched per channel per call (default: `200`) |
| `SLACK_COMPACT` | No | `false` to disable compact output (default: `true`) |
| `SLACK_LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |
| `DISABLED_TOOLS` | No | Comma-separated list of tool names to hide |

## Multiple workspaces

A single Slack token only sees the workspace it was minted in. To read more
than one workspace, keep your existing `SLACK_TOKEN` / `SLACK_USER_TOKEN` as
the **primary** and add the rest through `SLACK_WORKSPACES`, a JSON array:

```jsonc
SLACK_WORKSPACES=[
  { "name": "secondary", "user_token": "xoxp-...", "bot_token": "xoxb-..." }
]
```

- `name` is a label only — it appears as a `[name]` heading in merged
  output and is never used as a credential key. Workspace names live in the
  JSON **values**, so adding a workspace never introduces a new env var.
- Each entry needs at least one token (`user_token` for unread/mentions).
  `channels` is an optional comma-separated allow-list, same as
  `SLACK_CHANNELS`.

With more than one workspace configured, `get_unread_summary` and
`get_mentions` merge every workspace automatically, each under its own
`## [name]` heading. Pass `workspace: "<name>"` to either tool to scope the
call to one workspace. Drill-in tools (`get_channel_digest`, `post_message`,
`mark_read`, …) operate on the primary workspace.

## Docker

```bash
docker run -d --name slack-mcp \
  -e SLACK_USER_TOKEN=xoxp-your-user-token \
  -p 8001:8000 \
  velesnitski/slk-mcp
```

Add `-e SLACK_TOKEN=xoxb-...` alongside if you also want a bot token (Setup B).

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

A `Makefile` wraps the common flows: `make build` (version-stamped), `make test`,
and `make install` (build + sync the `/mcp` dialog label to `slack v<version>`).
The `/mcp` listing shows each server by its **config key** in `~/.claude.json`
(read at session start), not the server-reported name — `make sync-label`
keeps that key current so the dialog never displays a stale version. After
`make install`, **restart** Claude Code (a `/mcp` reconnect isn't enough). ADR 024.

## Compatibility (1.0)

SemVer applies to the **machine surface**: tool names, argument
names/types/defaults, call semantics, and environment-variable names. A
breaking change to any of these bumps the major version.

The **rendered output text is not contractual** — digests, summaries,
headers, and section labels target an LLM and will keep changing for
density in minor/patch releases. Do not regex the human-readable body;
integrate against the typed arguments and the structured tokens the
tools emit (permalinks, the `cursor:` timestamp, issue IDs under
`include_refs`). See ADR 037.

## License

MIT

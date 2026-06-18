# slk-mcp

Slack MCP server written in Go.

## Project structure

- `main.go` — entry point, CLI flags, transport selection
- `internal/config/` — env-based configuration
- `internal/slack/` — Slack Web API client and message formatting
- `internal/tools/` — MCP tool registration and handlers

## Development

```bash
go build -o slk-mcp .
SLACK_TOKEN=xoxb-test ./slk-mcp -transport stdio

# or via Makefile:
make build        # compile (version stamped via -ldflags from main.go)
make test         # go test ./...
make install      # build + sync the /mcp dialog label to "slack v<version>", then RESTART Claude Code
```

The `/mcp` dialog labels servers by their **config key** in `~/.claude.json`,
not by the server-reported `serverInfo.name`, and reads it at **session
start** — so after `make install` you must **restart** Claude Code (a `/mcp`
reconnect only re-runs the server, it doesn't re-read the key). `make
sync-label` (`scripts/sync-mcp-label.py`) keeps that key in sync with the
built version so the dialog never shows a stale version — see ADR 024.

## Rules

- Never commit real Slack tokens, workspace names, or channel names
- Use generic placeholders in tests and docs
- All secrets via environment variables at runtime
- Branch workflow: develop in `dev`, merge to `main`

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
```

## Rules

- Never commit real Slack tokens, workspace names, or channel names
- Use generic placeholders in tests and docs
- All secrets via environment variables at runtime
- Branch workflow: develop in `dev`, merge to `main`

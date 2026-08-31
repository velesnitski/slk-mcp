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
- Run `make hooks` once per clone. It installs a `pre-push` hook that runs
  the full sweep (`--history`) on every push, so the deny-list scan cannot
  be forgotten and never has to leave this machine — CI only ever runs the
  shapes-only scan (ADR 083)
- `make sweep` / `make sweep-history` run it by hand. It FAILS CLOSED: no
  `.sweep-patterns.local` means exit 2, never a green pass. Copy
  `.sweep-patterns.example` to create it; it is gitignored on purpose —
  see ADR 076 and ADR 079
- Credential-shaped test fixtures are assembled at runtime, never written
  as literals, and the tree scan honours no exemption — ADR 082

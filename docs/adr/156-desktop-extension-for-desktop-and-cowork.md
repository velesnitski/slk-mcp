# 156. Desktop extension for Claude Desktop and Cowork

Date: 2026-11-27

## Status

Accepted

## Context

slk-mcp installed only through Claude Code (`claude mcp add`). Claude
Desktop and Cowork, on macOS and Windows, have no `/mcp` command; they
take local servers as desktop extensions (`.mcpb` bundles), or remote
servers as custom connectors reachable from Anthropic's cloud.

A remote connector does not fit: the server would have to be exposed to
the internet, its HTTP transport has no authentication, and it holds the
user's Slack token.

## Decision

- `packaging/mcpb/manifest.json` (MCPB manifest 0.3, binary server)
  declares the settings form: the user token (required) and extra
  workspaces JSON, both `sensitive` so the app stores them in the system
  keychain; read-only and locale as optional fields. They map to
  `SLACK_USER_TOKEN`, `SLACK_WORKSPACES`, `SLACK_READ_ONLY`,
  `SLACK_LOCALE`.
- `scripts/build-mcpb.sh` (`make mcpb`, macOS) builds one bundle for
  both systems: a universal Mach-O (arm64 + amd64, joined with lipo) and
  a Windows amd64 `.exe` chosen through `platform_overrides.win32`.
  Windows on ARM runs the amd64 binary under emulation.
- The bundle is built locally at release time and attached to the GitHub
  Release, so CI needs no macOS runner.
- `mcpb_test.go` keeps the manifest honest: every env var it sets is read
  by the config, every `${user_config.x}` is declared, credential fields
  are sensitive, and win32 runs the `.exe`.
- docs/INSTALL.md gets a Desktop/Cowork section and says why the remote
  connector route is not supported.

## Consequences

- Desktop and Cowork users install by opening one file and filling a
  form; no JSON, no terminal.
- The binaries are ad-hoc signed (macOS) and unsigned (Windows); an
  enterprise policy that requires signed extensions will block them.
- Releases need `make mcpb` and an upload of `dist/slk-mcp-<v>.mcpb`.

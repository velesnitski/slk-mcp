# 157. One-click app links and a Cowork setup guide

Date: 2026-11-27

## Status

Accepted

## Context

docs/INSTALL.md told an agent to generate the pre-filled create-app link
with `make manifest-url` or a curl and Python one-liner. A Cowork or
Claude Desktop agent may have no shell, no repo checkout and no network
from its sandbox, so it could not produce the link. Cowork users also
had no prompt to start from.

## Decision

- docs/INSTALL.md carries the two create-app links (full, read-only)
  ready to click. They encode the manifests with comment and blank lines
  removed, which keeps them near 1.5 KB; `make manifest-url` does the
  same transform.
- `manifest_link_test.go` decodes every create-app link in README and
  INSTALL and fails unless each matches a manifest and each manifest has
  a link.
- A "Cowork or Claude Desktop" section gives the prompt to paste and the
  agent's steps: ask full or read-only, give the link and the Slack
  clicks, fetch or link `releases/latest/download/slk-mcp.mcpb`, the
  token form, then verify in a new task. The agent never installs for
  the user and never asks for the token.
- Every release attaches `slk-mcp.mcpb` (stable name) and
  `slk-mcp-<version>.mcpb`, so the latest-download link never breaks.

## Consequences

- The person's part is one link, four clicks in Slack, one file to open
  and one paste, with or without an agent.
- A release without the `.mcpb` assets breaks the stable link; uploading
  them is part of the release steps.

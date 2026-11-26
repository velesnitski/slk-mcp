# 151. App manifests and an agent-readable install guide

Date: 2026-11-27

## Status

Accepted

## Context

- Quick start had people tick scopes by hand from a README list. The list
  had drifted: the code also uses `files:read`, `lists:read`,
  `users.profile:write`, `users:write`, `dnd:write`, `im:write` and the
  channel write scopes, so a fresh install failed on those tools with
  `missing_scope`.
- Users increasingly hand the repo link to a coding agent and ask it to
  install the server. The agent cannot create a Slack app or copy a token,
  and nothing told it which steps are the person's, or that the token must
  not pass through the chat.
- The install commands pulled `velesnitski/slk-mcp` from Docker Hub. No
  such image is published, so they failed.

## Decision

- Two Slack app manifests in the repo root: `slack-app-manifest.yaml`
  (every user-token scope the code uses) and
  `slack-app-manifest.readonly.yaml` (no write scope except `im:write`,
  which only opens a DM for `transcribe_audio` latest-mode). Both keep
  token rotation off: a rotating token expires in 12 hours and the server
  reads it once.
- `manifest_test.go` scans non-test code for scope names and fails when
  the full manifest lacks one; `channels:manage`, the bot-token form, is
  satisfied by `channels:write`. It also keeps write scopes out of the
  read-only manifest.
- `docs/INSTALL.md` is written for a person and an agent at once: who does
  each step, `make manifest-url` for a one-click pre-filled
  `api.slack.com/apps?new_app=1&manifest_yaml=` link, token entry through
  `read -s` in the user's own terminal, a verification table, scope
  upgrades and uninstall.
- Install from source (`go install`) or a locally built image; the
  compose file builds locally and binds to `127.0.0.1`.

## Consequences

- A new scope used in code without a manifest update fails `go test`.
- The hand-maintained README scope list for Setup A is gone; the manifest
  is the single source.

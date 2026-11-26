# Install slk-mcp from scratch

This page is written for two readers: a person setting slk-mcp up by hand,
and a coding agent (Claude Code, Cursor, Copilot…) that was handed the repo
link and told "install this Slack MCP for me". Each step says who does it.

> **Agent: read this first.** You can do most of the work, but three steps
> happen in the user's browser and you cannot do them: creating the Slack app,
> clicking **Install**, and copying the token. Prepare everything around
> them, hand the user a one-click link, and **never ask the user to paste the
> token into the chat**: it would land in your context and transcript. The
> token goes from the browser straight into the user's own terminal (step 4).

## What you end up with

- A Slack app in your workspace that holds a **user token** (`xoxp-…`):
  slk-mcp acts as you, sees what you see, needs no channel invites.
- The `slk-mcp` binary on your machine.
- An MCP server entry in your client, verified with a real call.

Time: about 5 minutes, two of them in the browser.

## 1. Build the binary (agent or person)

Needs [Go 1.26+](https://go.dev/dl/) (`go version` to check).

```bash
go install github.com/velesnitski/slk-mcp@latest
"$(go env GOPATH)/bin/slk-mcp" -version
```

Or from a clone: `git clone https://github.com/velesnitski/slk-mcp && cd
slk-mcp && make build` (the binary lands in the repo root as `./slk-mcp`).

Prefer containers? Build the image locally: `docker build -t slk-mcp .`
(no image is published to a registry).

**Agent:** remember the absolute binary path. The MCP config needs it, and
a client does not run with your shell's `PATH`.

## 2. Pick a manifest (agent decides, person confirms)

A Slack [app manifest](https://docs.slack.dev/app-manifests/) is a file that
describes the app and every OAuth scope it gets, so nobody has to tick scopes
by hand.

| Manifest | Use when | Pair with |
|---|---|---|
| [`slack-app-manifest.yaml`](../slack-app-manifest.yaml) | Full tool set: read, post, react, status, canvases, user groups | – |
| [`slack-app-manifest.readonly.yaml`](../slack-app-manifest.readonly.yaml) | Digests, search, commitments, transcription; cannot change anything | `SLACK_READ_ONLY=true` |

If unsure, **ask the user**. Read-only is the safer default for a first
install; the app can be upgraded later (step 6).

A test (`manifest_test.go`) fails the build when the code uses a scope the
full manifest lacks, so the manifest is the complete list. What each scope
means: [Slack scopes reference](https://docs.slack.dev/reference/scopes).

## 3. Create and install the Slack app (person, in the browser)

**Agent:** generate a one-click link with the manifest pre-filled
([Slack supports this](https://docs.slack.dev/app-manifests/configuring-apps-with-app-manifests)),
and give it to the user:

```bash
make manifest-url                                   # full manifest
make manifest-url MANIFEST=slack-app-manifest.readonly.yaml
```

Without the repo checked out:

```bash
curl -fsSL https://raw.githubusercontent.com/velesnitski/slk-mcp/main/slack-app-manifest.yaml \
  | python3 -c 'import sys,urllib.parse;print("https://api.slack.com/apps?new_app=1&manifest_yaml="+urllib.parse.quote(sys.stdin.read()))'
```

**Person:**

1. Open the link (or go to [api.slack.com/apps](https://api.slack.com/apps)
   → **Create New App** → **From a manifest** and paste the file).
2. Pick the workspace → **Next** → review the scopes → **Create**.
3. **Install to Workspace** → **Allow**. If your workspace requires admin
   approval for apps, this becomes **Request to Install**; wait for the
   approval, then come back.
4. Open **OAuth & Permissions** and copy the **User OAuth Token** (starts
   with `xoxp-`). Do not paste it anywhere except step 4.

Token rotation is off in both manifests on purpose: a rotating token
[expires every 12 hours](https://docs.slack.dev/authentication/using-token-rotation),
and an MCP server reads its token once at start.

## 4. Register the server (person, in their own terminal)

**Agent:** print this command with the real binary path filled in and ask the
user to run it in a terminal **outside** the chat. `read -s` takes the token
without echoing it or saving it to shell history.

Claude Code ([MCP docs](https://code.claude.com/docs/en/mcp)):

```bash
read -rs SLACK_USER_TOKEN && \
claude mcp add slack --scope user \
  -e SLACK_USER_TOKEN="$SLACK_USER_TOKEN" \
  -- /absolute/path/to/slk-mcp -transport stdio; \
unset SLACK_USER_TOKEN
```

(Paste the token, press Enter.) For the read-only app add
`-e SLACK_READ_ONLY=true` before `--`.

Other clients take the same command and environment in their JSON config;
see the [MCP client list](https://modelcontextprotocol.io/clients). The shape:

```json
{
  "mcpServers": {
    "slack": {
      "command": "/absolute/path/to/slk-mcp",
      "args": ["-transport", "stdio"],
      "env": { "SLACK_USER_TOKEN": "xoxp-…" }
    }
  }
}
```

The token sits in that config file in plain text, like any MCP server's
credentials: keep the file readable by you only.

## 5. Verify (agent)

1. **Restart the client** (Claude Code reads its server list at session
   start; a `/mcp` reconnect is not enough after adding a server).
2. `claude mcp list` should show `slack … ✓ Connected`.
3. Make one real call, e.g. *"summarise my unread Slack messages"*
   (`get_unread_summary`). An answer with channels or "nothing unread" means
   it works.

| Symptom | Cause | Fix |
|---|---|---|
| `not_authed` / `invalid_auth` | Wrong or revoked token, or the bot token (`xoxb-`) was copied | Copy the **User** OAuth Token again; rerun step 4 |
| A tool says `missing_scope` and names a scope | The app was created from an older or read-only manifest | Step 6 |
| Unread or mention tools are missing | Only a bot token is set | Set `SLACK_USER_TOKEN` |
| Server not listed after adding | Client not restarted | Restart it |
| `transcribe_audio` returns a file path and a hint | whisper.cpp / ffmpeg not installed | [Voice notes](../README.md#how-to-transcribe-voice-messages) |

## 6. Change scopes later (person)

[api.slack.com/apps](https://api.slack.com/apps) → your app → **App
Manifest** → paste the newer manifest → **Save** → Slack asks you to
**reinstall** the app. Reinstalling keeps the same token in most cases; if
the page shows a new one, rerun step 4.

## More than one workspace

A token works in one workspace only. Repeat step 3 in each workspace (the
same manifest), then add the extra tokens through `SLACK_WORKSPACES`; see
[Multiple workspaces](../README.md#multiple-workspaces). In step 4, read
each token with its own `read -rs`.

## Optional extras

- Voice notes and huddles: [whisper.cpp + ffmpeg](../README.md#how-to-transcribe-voice-messages).
- Scheduled digests without a chat client: [headless mode](../README.md#headless-mode-scripts-and-schedulers).
- Every setting: [environment variables](../README.md#environment-variables).
- Shared bot instead of personal user token: [Setup B](../README.md#setup-b--bot-token-shared-team-use).

## Uninstall

`claude mcp remove slack --scope user`, then
[api.slack.com/apps](https://api.slack.com/apps) → your app → **Settings** →
**Delete App** (this revokes the token).

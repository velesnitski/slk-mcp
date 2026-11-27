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

## Cowork or Claude Desktop: ask Claude to set it up

No terminal needed. Paste this into a Cowork task (or a Desktop chat); it
verifies the download first, then walks you through the install. It is
not tied to any version, so it works for anyone you send the repo to:

```text
Install the Slack MCP from https://github.com/velesnitski/slk-mcp for me.
Follow docs/INSTALL.md, sections "Cowork or Claude Desktop" and
"Verify the download".

Rules: don't install anything yourself, never ask for my Slack token,
and keep it out of the chat.

1. Download the latest slk-mcp.mcpb and SHA256SUMS from the releases
   page, check the SHA-256, and show me what's inside the bundle and its
   manifest.
2. Check the build provenance (gh attestation verify, or the GitHub API
   route). If your sandbox can't, give me the exact command to run in my
   own terminal, with the hash I should expect.
3. Ask me whether I want the full or the read-only app (suggest
   read-only), then give me the matching "Create the Slack app" link and
   the clicks in Slack.
4. Tell me how to open the .mcpb and what to put in the install form:
   I paste the token myself, tick Read-only if I chose it, and set the
   locale (en-US for US dates).
5. Then I'll start a new task and ask for my unread Slack messages. If it
   fails, use the troubleshooting table in the guide.

End with a one-line verdict on whether the file checked out.
```

**Agent, in Cowork or Desktop:** you cannot install an extension into the
app, click buttons in Slack, or read the token, and you must not ask for
it. Do this, in order:

1. Ask whether the user wants the **full** or the **read-only** app
   (read-only if unsure).
2. Give the matching **Create the Slack app** link from step 3 below and
   the four clicks under *Person* there (pick workspace → Create →
   Install → copy the User OAuth Token). Tell them to keep the token on
   the clipboard and not paste it into the chat.
3. If you can write files to the user's folder, download
   `https://github.com/velesnitski/slk-mcp/releases/latest/download/slk-mcp.mcpb`
   there and tell them to open it from Finder or Explorer. Otherwise give
   them that link to download and open. If you can run commands, verify
   it first (see *Verify the download* below) and tell the user the result.
4. The app shows an install dialog with a form: **Install**, then paste
   the token into *Slack user token*; for read-only tick *Read-only*; for
   US dates set *Date and time format* to `en-US`.
5. Ask them to start a **new** task or chat (tools load at start) and
   request *"summarise my unread Slack messages"*. Use the troubleshooting
   table in step 5 if it fails.

Total for the person: one link, four clicks in Slack, one file to open,
one paste.

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

**One click, manifest pre-filled**
([Slack supports this](https://docs.slack.dev/app-manifests/configuring-apps-with-app-manifests)).
**Agent:** give the user one of these two links as is:

- [**Create the Slack app – full tool set**](https://api.slack.com/apps?new_app=1&manifest_yaml=display_information%3A%0A%20%20name%3A%20slk-mcp%0A%20%20description%3A%20Personal%20Slack%20MCP%20server%20-%20unread%20digests%2C%20commitments%2C%20search%2C%20posting.%0A%20%20background_color%3A%20%22%232c2d30%22%0Aoauth_config%3A%0A%20%20scopes%3A%0A%20%20%20%20user%3A%0A%20%20%20%20%20%20-%20channels%3Ahistory%0A%20%20%20%20%20%20-%20channels%3Aread%0A%20%20%20%20%20%20-%20groups%3Ahistory%0A%20%20%20%20%20%20-%20groups%3Aread%0A%20%20%20%20%20%20-%20im%3Ahistory%0A%20%20%20%20%20%20-%20im%3Aread%0A%20%20%20%20%20%20-%20mpim%3Ahistory%0A%20%20%20%20%20%20-%20mpim%3Aread%0A%20%20%20%20%20%20-%20channels%3Awrite%0A%20%20%20%20%20%20-%20groups%3Awrite%0A%20%20%20%20%20%20-%20im%3Awrite%0A%20%20%20%20%20%20-%20mpim%3Awrite%0A%20%20%20%20%20%20-%20users%3Aread%0A%20%20%20%20%20%20-%20users%3Aread.email%0A%20%20%20%20%20%20-%20search%3Aread%0A%20%20%20%20%20%20-%20files%3Aread%0A%20%20%20%20%20%20-%20reactions%3Aread%0A%20%20%20%20%20%20-%20reactions%3Awrite%0A%20%20%20%20%20%20-%20canvases%3Aread%0A%20%20%20%20%20%20-%20canvases%3Awrite%0A%20%20%20%20%20%20-%20lists%3Aread%0A%20%20%20%20%20%20-%20usergroups%3Aread%0A%20%20%20%20%20%20-%20usergroups%3Awrite%0A%20%20%20%20%20%20-%20chat%3Awrite%0A%20%20%20%20%20%20-%20users.profile%3Awrite%0A%20%20%20%20%20%20-%20users%3Awrite%0A%20%20%20%20%20%20-%20dnd%3Awrite%0Asettings%3A%0A%20%20org_deploy_enabled%3A%20false%0A%20%20socket_mode_enabled%3A%20false%0A%20%20token_rotation_enabled%3A%20false%0A)
- [**Create the Slack app – read-only**](https://api.slack.com/apps?new_app=1&manifest_yaml=display_information%3A%0A%20%20name%3A%20slk-mcp%20%28read-only%29%0A%20%20description%3A%20Personal%20Slack%20MCP%20server%20-%20read-only%20digests%2C%20search%20and%20commitments.%0A%20%20background_color%3A%20%22%232c2d30%22%0Aoauth_config%3A%0A%20%20scopes%3A%0A%20%20%20%20user%3A%0A%20%20%20%20%20%20-%20channels%3Ahistory%0A%20%20%20%20%20%20-%20channels%3Aread%0A%20%20%20%20%20%20-%20groups%3Ahistory%0A%20%20%20%20%20%20-%20groups%3Aread%0A%20%20%20%20%20%20-%20im%3Ahistory%0A%20%20%20%20%20%20-%20im%3Aread%0A%20%20%20%20%20%20-%20mpim%3Ahistory%0A%20%20%20%20%20%20-%20mpim%3Aread%0A%20%20%20%20%20%20-%20users%3Aread%0A%20%20%20%20%20%20-%20users%3Aread.email%0A%20%20%20%20%20%20-%20search%3Aread%0A%20%20%20%20%20%20-%20files%3Aread%0A%20%20%20%20%20%20-%20reactions%3Aread%0A%20%20%20%20%20%20-%20canvases%3Aread%0A%20%20%20%20%20%20-%20lists%3Aread%0A%20%20%20%20%20%20-%20usergroups%3Aread%0A%20%20%20%20%20%20-%20im%3Awrite%0Asettings%3A%0A%20%20org_deploy_enabled%3A%20false%0A%20%20socket_mode_enabled%3A%20false%0A%20%20token_rotation_enabled%3A%20false%0A)

These links are the manifests above with comments removed; a test
(`manifest_link_test.go`) fails if they drift. To regenerate:
`make manifest-url` (add `MANIFEST=slack-app-manifest.readonly.yaml`
for read-only).

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
`-e SLACK_READ_ONLY=true` before `--`. For US date and time formats
(`11/27`, `3:04 PM`, "by 12/3" read as December 3) add
`-e SLACK_LOCALE=en-US`.

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

### Claude Desktop and Cowork (macOS and Windows)

Desktop and Cowork have no `/mcp` command and no `claude mcp add`; they
install local servers as **desktop extensions** (`.mcpb` files). One file
serves both systems: it carries a universal macOS binary (Apple silicon
and Intel) and a Windows `.exe`, and the app picks the right one.

1. **Person:** download `slk-mcp-<version>.mcpb` from the
   [latest release](https://github.com/velesnitski/slk-mcp/releases/latest)
   (or build it on a Mac with `make mcpb`, which writes it to `dist/`).
2. **Person:** open it (double-click), or in the app go to
   **Settings → Extensions** and install it from there; see
   [installing desktop extensions](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop).
3. **Person:** the app shows a settings form. Paste the **User OAuth
   Token** from step 3 into *Slack user token*. Optional: *More
   workspaces* (a `SLACK_WORKSPACES` JSON array), *Read-only*, and
   *Date and time format* (`en-US` for `11/27` and `3:04 PM`).
   Credential fields are marked sensitive, so the app keeps them in the
   system keychain rather than in a config file, and they never pass
   through a chat.
4. **Agent or person:** in a new chat or Cowork task, ask *"summarise my
   unread Slack messages"*. In Cowork, the extension can be switched on
   or off per task from the **+** menu in the message box.

#### Verify the download

The `.mcpb` in each release is built by GitHub Actions from the release
tag (not on anyone's laptop) and carries a signed
[build provenance attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds).
To prove a file came from this repository, at that tag, built by the
release workflow:

```bash
gh attestation verify slk-mcp.mcpb -R velesnitski/slk-mcp
```

A pass prints the repository, the workflow and the commit it was built
from; compare the commit with the tag on the
[releases page](https://github.com/velesnitski/slk-mcp/releases). Without
the GitHub CLI, compare the file's SHA-256 with `SHA256SUMS` in the same
release (`shasum -a 256 slk-mcp.mcpb` on macOS,
`Get-FileHash slk-mcp.mcpb` in PowerShell); that proves only that the
download is intact, not where it came from.

**Without the GitHub CLI** (a Cowork sandbox, for instance), GitHub's
public API returns the attestation for a file's digest, no sign-in
needed:
`https://api.github.com/repos/velesnitski/slk-mcp/attestations/sha256:<SHA-256 of the file>`.
Base64-decode `dsseEnvelope.payload` of the first attestation;
`predicate.buildDefinition` names the workflow, the tag and the commit.
This confirms GitHub holds an attestation for exactly this file from this
repository's release workflow, but does not check the signature itself:
weaker than `gh attestation verify`, enough for a reasonable decision.

**Ask Cowork to verify it.** Paste this into a Cowork task:

```text
Verify the slk-mcp desktop extension before I install it. Don't install
anything and don't ask for my Slack token.

1. Download
   https://github.com/velesnitski/slk-mcp/releases/latest/download/slk-mcp.mcpb
   and https://github.com/velesnitski/slk-mcp/releases/latest/download/SHA256SUMS
   into my folder, and check the file's SHA-256 against SHA256SUMS.
2. If the gh CLI is available, run:
   gh attestation verify slk-mcp.mcpb -R velesnitski/slk-mcp
   Otherwise fetch
   https://api.github.com/repos/velesnitski/slk-mcp/attestations/sha256:<the file's SHA-256>,
   base64-decode dsseEnvelope.payload of the first attestation and read
   predicate.buildDefinition.
3. Report the repository and workflow that built it (should be
   velesnitski/slk-mcp, .github/workflows/release.yml), the tag
   (refs/tags/v…), the commit, and whether that commit matches the tag on
   https://github.com/velesnitski/slk-mcp/releases. Say which method you used.
4. Unzip the .mcpb and show me manifest.json: what it asks for, that the
   token field is marked sensitive, and that the bundle holds only
   manifest.json, LICENSE and server/.

Give a one-line verdict at the end.
```

A Cowork sandbox may block both routes: `gh` then fails with "no valid
Sigstore verifiers could be initialized", and the API call is refused by
the sandbox proxy. Cowork can still check the checksum and the bundle;
for the provenance it should hand you the `gh attestation verify` command
to run in your own terminal, on the same file (compare the SHA-256 it
reported).

A good result: the checksum matches; the workflow is
`velesnitski/slk-mcp/.github/workflows/release.yml` on the release tag;
the commit equals the one the tag points to; the bundle holds only
`manifest.json`, `LICENSE` and `server/`.

**Agent:** if you can run commands, run the verification before telling
the user to open the file, and report the commit it names. To avoid
trusting any binary, build it yourself on a Mac: `make mcpb` (Go and
Xcode command-line tools) writes `dist/slk-mcp-<version>.mcpb`.

Do **not** expose slk-mcp as a remote **custom connector** (a URL) for
Cowork: Anthropic's cloud would have to reach the server over the
internet, the HTTP transport has no authentication, and the server holds
your Slack token. Use the desktop extension.

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

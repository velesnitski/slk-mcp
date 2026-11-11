# ADR 136: headless mode – run any tool once from the command line

Date: 2026-11-11
Status: accepted

## Context

slk-mcp answers only inside an MCP session. A scheduled job – a
team-notes pipeline, a morning brief, another bot that reads Slack every
half hour – cannot use what the tools already do well: the unread sweep
with its delta cursor, commitments with deadlines, voice-note and huddle
transcription, several workspaces at once. Re-implementing them in each
consumer would fork the logic.

## Decision

Two subcommands on the same binary: `slk-mcp tools` lists the tools and
`slk-mcp run <tool> [key=value …] [--args JSON|-] [--json] [--timeout]`
calls one.

- **In-process MCP, nothing per tool.** The command builds the server
  exactly as the transports do and sends it `tools/list` and
  `tools/call` through `HandleMessage`. Every tool is reachable the day
  it is registered, and `SLACK_READ_ONLY` and `DISABLED_TOOLS` gate the
  command line exactly as they gate a client: a write tool under
  read-only does not exist.
- **Arguments typed by the schema.** `key=value` pairs become numbers,
  booleans or JSON according to the tool's input schema; a key the tool
  does not take, a malformed value or a missing required argument is a
  usage error that names the valid keys. `--args` takes a JSON object or
  `-` for stdin; pairs override it. Flags may sit between pairs.
- **Output for machines.** Plain text by default; `--json` prints one
  object with stable fields (`tool`, `args`, `ok`, `text`, `cursor`,
  `images`). `cursor` is the token of the last `cursor: …` line, which
  is how the unread summary hands over its delta, so a scheduler stores
  it and passes it back as `after=`.
- **Exit codes:** 0 success, 1 the tool reported an error, 2 a usage or
  configuration error. Logs go to stderr at `warn` by default, so stdout
  holds only the result.

## Consequences

- A scheduler gets the tools without an MCP client and without a second
  implementation. For read-only jobs, `SLACK_READ_ONLY=true` removes the
  write tools entirely rather than trusting the caller.
- The command runs one call and exits; anything stateful between runs
  (the cursor) is the caller's to keep.
- The in-process call skips the transport. `TestIntegration_Headless…`
  calls the same tools over the streamable-HTTP transport and through
  `run`, against one fake Slack, and requires the same text byte for
  byte – for a success and for a tool error – plus the unread cursor
  lifted into `--json`. `headlessWith` takes the configuration so the
  test can point it at the fake; `APIURL` stays unreachable from the
  environment.

# ADR 124: edit_canvas — refuse rather than guess

Date: 2026-10-16
Status: accepted

## Context

`read_canvas` reads canvases, but nothing could write one. Team notes,
checklists and running agendas live in canvases, so updating them meant
leaving the client. Slack exposes `canvases.edit` (insert at start/end,
insert before/after a section, replace, delete, rename) and
`canvases.sections.lookup` (find section ids by text and heading level).

An edit cannot be undone through the API, and a canvas is usually a
shared document. The failure that matters is not an error message but a
correct-looking edit in the wrong place or the wrong canvas.

## Decision

- **One tool, `edit_canvas`,** registered through `toolDef` with
  `Writes` and `RequiresUserToken`: hidden in `SLACK_READ_ONLY` and with
  a bot token only.
- **Edits run as the operator.** The user token is used, never the bot:
  canvas access follows people, and the canvas history should name the
  person who asked for the change.
- **Raw HTTP, not slack-go.** The SDK's change type has no
  `title_content`, so `rename` is impossible through it. The calls go
  through `CanvasService.callUser`, the same raw path `RecentCanvases`
  uses, with rate-limit retry (a 429 applies nothing) and Slack's
  `needed` scope and `response_metadata.messages` carried into the error.
- **Target resolution never guesses.** By id or link the file must be a
  canvas. Through a channel: the canvas tab; else the only shared canvas;
  else an error listing them, unless `match` / `date` (same rules as
  `read_canvas`) select one.
- **A section is addressed by text that matches exactly one section.**
  Zero or several matches change nothing and say how many matched;
  `section_type` (h1, h2, h3, any_header) narrows the lookup.
- **Whole-canvas replace needs `replace_all=true`.** `replace` without a
  section would otherwise overwrite the document silently.
- **Inputs are validated before any Slack call,** per operation.
- **`dry_run`** resolves the canvas and the section and reports the edit
  in one clause without writing.

## Consequences

- Canvases can be updated from the client with the same addressing as
  `read_canvas`.
- A user token needs the `canvases:write` scope; without it Slack's
  error names the scope.
- Creating a canvas is out of scope: a channel with no canvas is an
  error.
- One change per call. Several edits are several calls, each checked on
  its own.

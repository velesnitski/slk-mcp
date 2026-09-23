# ADR 110: a permalink names its workspace

Date: 2026-09-23
Status: accepted

## Context

`view_image` failed with `conversations.history: channel_not_found` on a
permalink copied from the second workspace. `get_message` resolved the
same link without trouble. Passing `workspace` explicitly made the image
tool work, so the data was reachable; the tool simply went to the wrong
place.

`get_message` routes by the permalink's host: `routeWorkspace` compares
the link's host with each workspace's `auth.test` URL and picks the
match. The file tools never used it. They share one entry point,
`fetchFiles`, which called `scopedWorkspace(workspace)` and therefore
sent every link to the primary unless told otherwise. One call site, so
five tools carried the defect at once: `view_image`, `read_document`,
`transcribe_audio`, `download_audio`, `analyze_audio_tone`.

A sweep of every handler that accepts a permalink found two more.
`delete_message` scoped the same way. `mark_read` had no workspace
support at all and said so in its description — "Operates on the primary
workspace" — so a link from the second workspace named a channel the
primary does not have.

None of these could act on the wrong conversation. Channel IDs are
workspace-scoped, so the primary answers `channel_not_found` rather than
touching something else. The cost was a tool that looked broken on
perfectly valid input, and a caller left to guess that `workspace` was
the missing piece.

## Decision

Every tool that accepts a permalink routes through `routeWorkspace`.
Precedence is unchanged from `get_message`: an explicit `workspace`
argument wins; otherwise the permalink's host picks the workspace; a
host no workspace owns falls back to the primary.

That fallback must not be silent. `routeWorkspace` already returns a
note for it; the file tools, `delete_message` and `mark_read` now append
that note to **error** results through `withRouteNote`. A failure after a
fallback therefore reads "…(no configured workspace matches host
"x.slack.com" — tried the primary)", which points at the cause. Success
results are left alone — they already carry the `[label]` suffix, and
announcing a routing that worked is noise.

`mark_read` gains the standard `workspace` argument and reports its
target through `conversationLabel`, which also removes the `##alpha`
rendering when the caller passes a channel with its sigil.

## Consequences

- A permalink from any configured workspace works in every tool that
  takes one, without the caller naming the workspace.
- Routing costs one `auth.test` per workspace for the life of the
  process; `TeamURL` reads the cached response.
- A workspace configured with a bot token only cannot self-identify, so a
  link to it still falls back to the primary. That case now says so on
  failure instead of returning a bare `channel_not_found`.
- The tests assert on which fake workspace received the call, not on the
  result text. When both workspaces fail the same way the text cannot
  tell them apart, and the call log is the only evidence of where the
  request went. Six of the seven fail against the previous code; the
  seventh guards that an explicit `workspace` still overrides the host.

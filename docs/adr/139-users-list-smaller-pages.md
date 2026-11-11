# ADR 139: users.list falls back to smaller pages on Slack's server errors

Date: 2026-11-11
Status: accepted

## Context

After the app gained `users:read.email` and was reinstalled, every
`users.list` call on a large workspace failed with `fatal_error`, and
with it every tool that reads the roster: `list_users`, mention
resolution, user groups. The same call with fewer profiles per page
succeeds. Slack documents 200 as the page size but does not promise to
serve it.

## Decision

`UserService.List` starts with 200 per page. When Slack answers with a
server-side error (`fatal_error`, `internal_error`, `request_timeout`)
it starts over at half the page size, down to 25, and returns the error
only when the smallest page fails too. Auth, scope and rate-limit
errors are not page-size problems and are returned at once.

## Consequences

- A workspace that needs small pages pays a few failed calls per
  listing plus more pages; correctness is unchanged.
- The retry restarts the listing rather than resuming from the failed
  cursor, because slack-go does not expose the cursor; listings are
  short enough that this does not matter.

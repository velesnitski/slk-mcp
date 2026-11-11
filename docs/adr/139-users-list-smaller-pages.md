# ADR 139: users.list retries a failed page and says where it broke

Date: 2026-11-11
Status: accepted

## Context

Right after the app was reinstalled with new scopes, every `users.list`
call failed with `fatal_error` for a few minutes, and with it every tool
that reads the roster: `list_users`, mention resolution, user groups.
The error arrived after almost two minutes with nothing else: no page,
no count, nothing to tell a transient Slack fault from a broken token.
The listing went through slack-go's `GetUsersContext`, which retries
only rate limits; any other failure threw away every page already read.

A first fix assumed the pages were too heavy and restarted the listing
with smaller ones. The logs showed no such retry was ever needed: the
next attempt simply succeeded. That fix was replaced before release.

## Decision

`UserService.List` pages by hand.

- A page that fails on Slack's side (`fatal_error`, `internal_error`,
  `request_timeout`) is requested again on its own cursor, three times
  in all; pages already read are kept. Auth, scope and rate-limit
  errors are not retried here (rate limits keep their own retry).
- A listing that still fails names the page, how many users were read
  and how long it ran: `users.list page 2 (after 200 users, 41s):
  fatal_error`.
- `list_usergroups` names members through cached `users.info` lookups
  when the roster cannot be read, instead of printing raw IDs.

## Consequences

- A transient Slack fault costs a retry of one page, not the listing.
- A persistent one fails with an error that points at where to look.

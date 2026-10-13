# ADR 120: email column and domain filter in list_users

Date: 2026-10-13
Status: accepted

## Context

Auditing which members are registered with an address on a given domain
— for example before members are exposed to an external organisation
through a shared channel — needed an admin export from the Slack UI. The
roster tool had every field but the email.

## Decision

- `with_email` adds `email=` to each row; an address Slack did not send
  is shown as `(not visible)`, never as an empty cell.
- `email_domain` keeps only users whose address is on that domain or a
  subdomain of it, matched on the host after the last `@` (`ru` matches
  `mail.ru` and `corp.example.ru`; it does not match `guru.com` or
  `ru.example.com`). Input is normalized (`@`, leading dot, case). It
  implies `with_email`.
- A coverage line follows: `emails visible: N of M`, counted on the
  roster before the domain filter. When Slack returned no emails at all
  the line says the token lacks `users:read.email` and that no domain
  check was possible — an empty result is otherwise indistinguishable
  from "nobody on that domain".

## Consequences

- Emails need the `users:read.email` scope on the token; README lists it
  as optional. Without it the tool still works and says why the column
  is empty.
- Emails are personal data; they are only rendered when asked for.

# ADR 138: user groups – one @handle for a recurring list of people

Date: 2026-11-11
Status: accepted

## Context

The same list of people gets tagged again and again – every team lead,
everyone on call. A list kept outside Slack cannot be pasted back as
mentions: the client turns typed text into a mention only through its
own autocomplete, so a pasted `@handle` or `<@U…>` arrives as text and
notifies nobody. ADR 137 fixes that for messages sent through
`post_message`, but not for messages a person types. Slack's answer is a
user group: one `@handle` that notifies all its members, kept in Slack
and usable by anyone in the client.

## Decision

Two tools and one extension.

- **`list_usergroups`** lists active groups with handle, name, ID and
  members by handle. Disabled groups are not listed.
- **`manage_usergroup`** creates a group or changes its members.
  - Members are given the way people think of them – handle, display
    name or real name, or a user ID – comma- or space-separated.
  - Every member must match exactly one person. An unknown or shared
    name refuses the whole edit and lists what to fix: a group edited on
    a guess would notify the wrong person every time it is used.
  - `mode=set` replaces the list, `add` and `remove` edit it. A group
    cannot be emptied (Slack refuses it); identical members are not
    written. `dry_run` shows the change without making it.
  - A handle that does not exist is created; its name defaults to the
    handle.
  - A write tool: hidden under `SLACK_READ_ONLY`.
- **`post_message` tags groups.** A word after `@` that no person
  answers to is looked up as a group handle and becomes
  `<!subteam^S…>`. Groups are read only when such a word exists, and a
  token without `usergroups:read` simply leaves it as text. A group
  matches by its handle only: display names like "Tech leads" read like
  prose and would catch ordinary words.
- The user token is used when present: a person's own rights decide who
  may edit groups, and the scopes are user scopes.

## Consequences

- Needs `usergroups:read` and `usergroups:write`, a reinstall of the
  app, and a paid Slack plan. A workspace may also allow only admins to
  edit groups; the error says so.
- Slack's errors for these cases are terse; the tool names the missing
  scope, the admin setting or the plan behind them.

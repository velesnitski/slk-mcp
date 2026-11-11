# ADR 143: read Slack's VIP list and starred conversations, keep them locally

Date: 2026-11-11
Status: accepted

## Context

ADR 142 put VIPs first from a configured list because Slack's VIP list
has no documented API. Keeping two lists in step by hand is error-prone,
and Slack also knows which conversations the operator starred. Both live
behind client endpoints Slack's own apps use: `users.prefs.get` for the
preferences (the VIP list among them) and `users.channelSections.list`
for the sidebar, where "Starred" is a section. The legacy `stars.list`
(scope `stars:read`) still lists starred conversations. None of these is
promised to accept a user token or to keep its shape; `client.counts`
(ADR 131) showed that such endpoints often do work with one.

## Decision

- **Read every source, use what answers.** The VIP list is taken from any
  preference whose name mentions "vip", as user IDs found in its value –
  the key and its encoding have changed between client versions. Starred
  conversations come from the sidebar's `stars` section, or from
  `stars.list` when sections are refused. Each source is reported; the
  read fails only when all of them do.
- **Kept locally, IDs only.** One file per workspace,
  `slack-prefs-<workspace>.json` under `SLACK_STATE_DIR` (default: the
  user cache directory + `/slk-mcp`; `off` disables it), written
  atomically with owner-only permissions. No message text, no tokens.
- **Fresh enough on its own.** A tool that needs the priorities refreshes
  a copy older than a day, within ten seconds; a refused refresh keeps
  the old copy and is not retried for thirty minutes. `sync_slack_prefs`
  refreshes on demand and says what it found and what was refused.
- **Used where ADR 142 uses VIPs.** Slack's VIPs join `SLACK_VIP_USERS`
  (Slack is the source, the variable adds to it). Starred conversations
  follow VIP ones in `get_unread_summary`, are marked `☆`, and are not
  collapsed as low-signal.

## Addendum (1.76.1): what Slack actually answers

Tried on two live workspaces with a user token: `users.prefs.get` needs
the legacy `read` scope and `stars.list` the `stars:read` scope; neither
is offered to apps created today (stars were retired for Saved items),
and `users.channelSections.list` refuses app tokens. So for most
operators Slack gives none of it. Therefore:

- A refusal that retrying cannot change (`missing_scope`,
  `not_allowed_token_type`, …) is stored like a result and asked again a
  day later, not every half hour. A transient failure is not stored.
- The tool says what each refusal means, and never tells the operator to
  add those scopes – they are not in the list.
- `SLACK_STARRED_CHANNELS` (names or IDs) sets starred conversations by
  hand, as `SLACK_VIP_USERS` does for people; both always apply.

## Consequences

- Without the state directory (tests, `off`) nothing is read or written.
- If Slack closes these endpoints to user tokens, the tool reports it
  and `SLACK_VIP_USERS` keeps working.

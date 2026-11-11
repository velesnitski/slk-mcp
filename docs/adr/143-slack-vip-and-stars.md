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

## Consequences

- Without the state directory (tests, `off`) nothing is read or written.
- If Slack closes these endpoints to user tokens, the tool reports it
  and `SLACK_VIP_USERS` keeps working.

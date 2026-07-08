# ADR 039: `set_status` — custom status + presence (AFK)

Date: 2026-07-08
Status: accepted

## Context

No tool touched the user's profile — no way to set "🌴 AFK till
tomorrow" or flip presence to away. slack-go exposes both
`users.profile.set` (custom status: text + emoji + expiration) and
`users.setPresence` (active/away), so this is a wiring task, but three
design decisions carry weight.

## Decision

Add `set_status` with these deliberate choices:

1. **You-global by default.** A custom status is a property of the
   PERSON, not a channel — you are away from every workspace at once.
   So an empty `workspace` arg fans out to ALL workspaces, the exact
   opposite of `post_message`'s single-target default. This is the one
   write tool where "empty = all" is correct, and the description says
   so loudly. A named label still targets one.

2. **Server owns the clock.** "AFK till next work day" needs an
   expiration timestamp, but the calling agent can't know the server's
   wall-clock precisely and the workflow sandbox has no `Date.now()`.
   So the tool takes `clear_after_minutes` and computes
   `status_expiration = now + minutes` server-side (`statusExpiry`,
   `now` injected for tests). 0 = no expiry, Slack's sentinel.

3. **Presence is opt-in and separable.** `set_presence` (default
   false) gates whether the dot is touched at all; `away` chooses the
   value. This lets you set a status without flipping presence, and
   flip presence without a status — the two Slack concepts stay
   independent instead of being conflated into one "AFK" switch.

Personal actions require a user token: the `StatusService` is built on
the USER client (nil on a bot-only workspace) and every method guards
on it, so a bot-only workspace reports "skipped: no user token" in the
fan-out rather than silently no-op'ing. The tool registers only when
some workspace has a user token and only when not `SLACK_READ_ONLY`
(it mutates the profile). `normalizeEmoji` accepts a bare name
("palm_tree") or colon form (":palm_tree:").

## Consequences

- "afk till tomorrow", "set my status to lunch for 45m", "clear my
  status" are one call, correct across both workspaces.
- New `StatusClient` contract + `StatusService`; the service is the
  first built exclusively on the user client (Search prefers it,
  Unread uses it, but Status *requires* it).
- Minor release under ADR 037 (additive tool). 514 → 518 tests.

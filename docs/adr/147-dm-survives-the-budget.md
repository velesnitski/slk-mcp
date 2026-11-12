# ADR 147: a direct message survives the max_chars budget

Date: 2026-11-12
Status: accepted

## Context

`get_unread_summary` renders conversations in rank order until the
`max_chars` budget is spent, then lists the rest in a footer as a name
and a count. A direct message got the same treatment as a noisy
channel.

In a real sweep the budget went to one long DM: `dm_window_hours`
brings in DMs with recent activity whether or not they are read, and a
DM shows at least 50 messages (ADR 128). That conversation – already
read, mostly the operator's own lines – filled the budget, and a short
DM from someone else with a new, important message ended up in the
footer as "(10)". Nothing said what was in it, so it was not opened.

## Decision

- **A DM is never reduced to a count.** When the budget is spent, a DM
  still gets one line: its label and volume, the newest message from
  the other side (or the newest at all) cut to 200 characters, and the
  `get_channel_digest` call that reads it in full. Channels keep the
  footer.
- **Pending DMs first.** After priority, VIP and starred conversations,
  a DM with a message or thread reply from the other side newer than
  `last_read` outranks every scored conversation.
- **Read DMs are short.** A DM with nothing new from the other side is
  in the sweep only because of the window; it shows its newest 5
  messages, not 50.

## Consequences

- A sweep can run past `max_chars` by one bounded line per DM that did
  not fit – a few hundred characters each.
- The 50-message minimum of ADR 128 now applies to DMs with something
  new; a DM already read shows its last few lines and the call to read
  more.

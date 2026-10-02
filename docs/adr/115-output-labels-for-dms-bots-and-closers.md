# ADR 115: labels for search-built DMs, bot authors, and closers

Date: 2026-10-02
Status: accepted

## Context

Three small output defects seen in daily use:

- A DM reconstructed from a search hit carries no `is_im` flag and the
  peer's user ID as its name, so the unread digest labelled it `#U0…` —
  a channel that does not exist and a name nobody can act on.
- `get_message` on a block-only app or workflow post printed
  `from:  at …`: the author lives in `bot_profile`, which was not read.
- `get_mentions pending_only drop_closing_acks` kept one-word closers in
  Russian ("Да", "Принял"), emoji-only replies (":flushed:"), and Slack's
  own "X has joined Slack – take a second to say hello" notice, which
  search attributes to the new member and so looked like an unanswered
  ping.

## Decision

- A non-IM, non-MPIM conversation whose name is a bare user ID is
  labelled `@<handle>`.
- Author fallback order: resolved user, username, `bot_profile.name`
  (suffixed "(bot)"), then `bot <BotID>`.
- The closing-ack set gains short Russian and English confirmations, a
  body made only of emoji shortcodes, and the join notice. The two-word
  heuristic and the question-mark guard are unchanged, so a confirmation
  followed by an ask ("Да, но когда релиз?") still surfaces.

## Consequences

- Fewer false "pending" items in mention sweeps; a reply that is only a
  shortcode is treated as a reaction, which is what it is.
- "Да" as a reply is now a closer; a "Да" that opens a new topic is
  rare enough to accept.

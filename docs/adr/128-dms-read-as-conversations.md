# ADR 128: DMs are read and shown as conversations

Date: 2026-10-23
Status: accepted

## Context

A sweep summary of a busy DM showed its 20 newest messages and a
trailing "+9 more messages". The lines shown were replies ("all the
information and keys will be handed over", "after the holiday he takes
it fully"); the messages they answered — the subject — were among the
nine cut. Read without them, the replies were attributed to an
unrelated topic. Four things contributed:

- **The per-channel cap treated a DM like a feed.** Cutting a channel to
  its newest messages loses old news; cutting a DM the same way loses
  the start of the conversation its newest lines answer.
- **The cut line did not say what was missing.** "+9 more messages" at
  the bottom read as a tail, not as the beginning of the exchange, and
  named no way to read it.
- **Merged mention hits broke the order.** `mergeThreadMentions` appended
  hits without re-sorting, so the "first N" the renderer keeps were not
  always the newest N.
- **Two channel heuristics fired inside DMs.** The ADR 127 note "you
  posted in this channel after it" annotated nearly every line of a DM,
  since every line follows one of the operator's. And the low-signal
  detector collapsed any conversation of five or more short messages
  into "N short status updates" — in a DM, short lines are the content.

## Decision

- **`slack.DMMinMessages` = 50.** The unread read of a DM asks for at
  least that many messages (plus the usual thread-parent headroom), the
  DM window reads that many, and the renderer shows that many, whatever
  the per-channel cap. A larger cap still applies.
- **The cut line names what is missing and how to read it:** "... +N
  older messages not shown — get_channel_digest channel=<id> reads the
  rest", with the workspace when more than one is configured
  (`format.WithTruncationHint`, `readMoreHint`).
- **`mergeThreadMentions` re-sorts newest first** after merging.
- **DMs never get the follow-up note** and are **never low-signal**.

## Consequences

- A busy DM costs more characters in the sweep; DMs were already the
  priority tier of the output budget.
- Any cut conversation states its cut and the call that reads the rest.

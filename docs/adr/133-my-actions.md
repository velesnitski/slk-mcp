# ADR 133: `get_my_actions` — commitments, deadlines and pings from Slack

Date: 2026-11-03
Status: accepted

## Context

The morning question is not "what is unread" but "what do I owe today,
and whom should I ping". The answers sit in Slack as ordinary messages:
"пришлю до пятницы", "until tmrw the write-up", "посмотри пожалуйста".
The unread sweep shows them once and forgets them, a read message never
comes back, and a deadline written as "tomorrow" is wrong by the next
morning unless someone converts it to a date.

## Decision

A tool, `get_my_actions`, built on a pure package `internal/actions`.

**Sources.** Per workspace, three searches run side by side: `from:me`
and the two mention queries (`to:me` and the operator's @handle, see
`slack.MentionQueries`), over the last `days` (default 7). Bot messages are
ignored.

**Classification** (phrase rules, Russian and English):

- the operator's own message that requests something of someone it is
  addressed to (a DM, or a message that @-mentions someone else) is
  *waiting*; otherwise one that promises ("сделаю", "пришлю", "will
  investigate", "in process") is a *promise*;
- a message to the operator that requests something, or names a
  deadline, is an *ask*; one that only asks a question is a *question*.

**Deadlines** are read relative to the message's own time: "завтра /
tomorrow / tmrw", "послезавтра", weekdays after a preposition ("до
пятницы", "until frriday", "на среду"), "через N дней", "this / next
week", dates after a preposition ("до 15.11", "до 10 ноября", "by Nov
20"), and "asap / срочно / горит" as due the same day. The earliest wins.
A bare "сегодня / today" counts only when nothing else does, because it
narrates ("сегодня обсуждали") more often than it sets a deadline.
Dates without a preposition — the "03.11.26" heading a daily report — are
not deadlines.

**Status** comes from what followed, read once per thread and once per
DM from its oldest commitment on: a done reaction, or a later message
from the responsible side that says done or carries a file, closes it;
any reply answers a question; any other reply from the responsible side
only acknowledges it, and the item stays open showing that reply. The
operator is responsible for promises, asks and questions; anyone else
for a waiting item. An own promise that is the reply to an ask in the
list is folded into the ask, which carries the deadline.

**Views.** `due=today` lists what is overdue or due today, plus undated
asks, questions and requests nobody moved for `stale_hours` (default 24)
— the ping list; `due=week` looks seven days ahead; `due=all` lists every
open item. Each line shows the deadline badge, the kind, who and where,
the text, the phrase the date was read from, the acknowledging reply if
any, and the permalink. Counts of closed, undated and unchecked items
follow.

No model is used: the rules are fast, free, deterministic and testable,
and the output says that an unusually worded commitment can be missed.

## Consequences

- One call answers "what do I owe today and whom do I ping".
- In a DM, a later reply is matched to every commitment before it, so
  "готово" about one thing can close another in the same DM. Channel
  commitments are judged from their thread only; a reply posted to the
  channel instead of the thread is not seen.
- A search page holds 100 hits; when the operator's own messages fill it,
  the output says older commitments may be missing.
- The calls are bounded: three searches, plus one read per thread and per
  DM holding a commitment, capped by `limit` (default 60) and the sweep
  deadline.

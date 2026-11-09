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

**Sources.** Per workspace, three searches run side by side, each read
up to five pages of 100 (an active operator sends several hundred
messages a week): `from:me`
and the two mention queries (`to:me` and the operator's @handle, see
`slack.MentionQueries`), over the last `days` (default 7). Bot messages are
ignored.

**Classification** (phrase rules, Russian and English):

- the operator's own message that requests something of someone it is
  addressed to (a DM, or a message that @-mentions someone else) is
  *waiting*; otherwise one that promises ("сделаю", "пришлю", "will
  investigate", "in process") is a *promise*;
- a message to the operator that requests something, or names a
  deadline and does not just say thanks, is an *ask*; one that only asks a question is a *question*.

**Not commitments**, whatever their words: messages from bot and app
users and DMs with them, notes to self, reports (over 2000 characters
or 15 lines — digests and status write-ups, whose "urgent" is content;
a commitment with a short list stays in), and a group
chat's messages unless they @-mention the operator. A notice of time
off ("возьму полдня больничного", "беру отгул") reuses promise verbs but
owes nothing, so it is not a promise.

**Deadlines** are read relative to the message's own time: "завтра /
tomorrow / tmrw", "послезавтра", weekdays after a preposition ("до
пятницы", "until frriday", "на среду"), "через N дней", "this / next
week", dates after a preposition ("до 15.11", "до 10 ноября", "by Nov
20"), and "asap / срочно / горит" as due the same day. The earliest wins.
A bare "сегодня / today" counts only when nothing else does, because it
narrates ("сегодня обсуждали") more often than it sets a deadline.
A deadline set with a preposition ("до пятницы", "until tmrw", "by
15.11") or as urgent is strong; a bare "завтра" or "в пятницу" is weak,
because it narrates as often ("доклад завтра", "открыли в пятницу"). Only
a strong deadline makes a message an ask by itself, and a strong deadline
beats an earlier weak one. Dates without a preposition — the "03.11.26"
heading a daily report — are not deadlines.

**Status** comes from what followed, read once per thread and once per
DM from its oldest commitment on: a done reaction, or a later message
from the responsible side that says done or carries a file, closes it;
any reply answers a question; any other reply from the responsible side
only acknowledges it, and the item stays open showing that reply. In
one DM or thread, an item's follow-ups stop at the next item of the
same kind from the same person that the other side answered something
before: a reply after the operator's next request answers that request,
not the one before it, while a clarification sent before any answer is
the same turn continued, and the answer after both settles both. An ask or a
question at a channel's top level is also answered by the operator's own
top-level post in that channel within three hours: such questions are
usually answered in the channel, not in a thread. An
ask or question put to several people is answered when any of them
other than the operator replies. An acknowledged item two days past its
date is no longer listed for today: the moment has passed. The
operator is responsible for promises, asks and questions; anyone else
for a waiting item. An own promise that is the reply to an ask in the
list is folded into the ask, which carries the deadline.

**Views.** `due=today` lists what is overdue or due today, plus undated
asks and questions made to the operator alone (a one-to-one DM, or a
channel message mentioning only them) and requests nobody moved for
`stale_hours` (default 24) — the ping list; Slackbot's notices are no
one's reply; `due=week` looks seven days ahead; `due=all` lists every
open item. Each line shows the deadline badge, the kind, who and where,
the text, the phrase the date was read from, the acknowledging reply if
any – the first reply, which is the one that answers it, or the reply
that closed it – and the permalink. Counts of closed, undated and unchecked items
follow.

No model is used: the rules are fast, free, deterministic and testable,
and the output says that an unusually worded commitment can be missed.

## Consequences

- One call answers "what do I owe today and whom do I ping".
- In a DM, a reply counts only until the next commitment of the same
  kind from the same person, so "готово" about one thing rarely closes
  another. A channel commitment is judged from its thread, plus, for a
  top-level ask or question, the operator's top-level posts in the next
  three hours; a later unrelated post is not taken for an answer.
- A query reads at most 500 hits; when a search fills them, the output
  names the date it actually reached back to, so the reader knows how
  much of the window was covered.
- The calls are bounded: three searches of up to three pages, plus one
  read per thread, per DM and per channel holding a commitment, capped by `limit` (default 60) and the sweep
  deadline.

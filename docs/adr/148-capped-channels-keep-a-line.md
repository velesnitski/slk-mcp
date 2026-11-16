# ADR 148: capped channels keep a line; links are not words

Date: 2026-11-16
Status: accepted

## Context

Two misreads in the operator's daily sweep.

**A capped channel was a count.** `get_unread_summary` renders
conversations in rank order until `max_chars` is spent and lists the
rest as "#name (N)". ADR 147 gave DMs one line past the cap; channels
kept the bare count. In a real sweep a monitoring channel was cut: most
of its messages were the operator's own automated status posts, which
rank low, but it also held a colleague's proposal that nobody had
answered. "(13+4 replies)" did not say that, so it was not opened.

**A links-only message was a question.** `get_my_actions` classifies a
message that addresses the operator by phrase rules; any "?" makes it a
question. A colleague mentioned the operator with three links (a plan,
a board, a design file) and nothing else – an FYI. A design-tool URL
carries `?node-id=…`, so the message was listed as a question waiting
for an answer for two days.

## Decision

- **Capped channels keep one line.** The first 8 channels cut by the
  budget, in rank order, are listed with their volume and the newest
  message by someone other than the operator – top-level or thread
  reply – cut to 160 characters. A message that @-mentions the operator
  is picked over a newer one that does not, and is marked. Bot posts,
  joins and the operator's own lines never stand for a channel.
- **Feeds stay counts.** Git, log and low-signal channels, channels
  hidden by `skip_*_mode`, channels where only the operator or bots
  wrote, and everything past the first 8 stay in the footer as before.
- **Link targets are dropped before the phrase rules.** A Slack link
  `<url|label>` reads as its label, `<url>` and a bare URL read as
  nothing. This applies to every rule in the actions package – the
  question mark, request and promise words, and deadlines – so a date
  or "today" in a URL path is not a deadline either.

## Consequences

- A capped sweep grows by at most 8 lines of ~200 characters.
- The operator's own posts in a channel are still not previewed, even
  when they carry the news (an automated status line). Such a channel
  is the operator's to read; a preview here answers "did anyone else
  say something".
- A question asked only in a link's label still counts; a question
  asked only in a URL does not.

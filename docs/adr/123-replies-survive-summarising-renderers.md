# ADR 123: replies survive summarising renderers; log detection tightened

Date: 2026-10-16
Status: accepted

## Context

A morning sweep showed a general-purpose channel as `[LOG MODE — 2 msgs]`
and nothing else. Replies in a thread the operator had taken part in —
found by the own-thread backstop — never appeared. Two defects
combined:

- The log, git and low-signal renderers summarise a channel's top-level
  messages and ignore `Replies` entirely, so any reply, however human,
  was dropped without a trace. `skip_log_mode` / `skip_git_mode` dropped
  the whole channel, replies included.
- The channel became a "log" on thin evidence: one automated post out of
  two new messages met the 50% bot-share rule. Separately, the name rule
  treated any channel named "…report(s)…" as a feed, so channels of
  hand-written status reports were always collapsed into a severity
  histogram.

## Decision

- **Replies follow the summary.** After a log, git or low-signal render,
  the channel's thread replies are appended as the same
  "+ N replies in earlier threads:" block the regular digest uses
  (`format.ThreadRepliesBlock`), threads ordered by parent time. An empty
  git feed no longer short-circuits the channel before its replies.
- **Skip flags hide the feed, not the people.** With `skip_log_mode` or
  `skip_git_mode`, a channel that has thread replies is rendered as its
  heading plus the replies; a pure feed is still skipped.
- **Sample floor.** The bot-share rule applies from 4 new messages up.
  Below that, a channel is a feed only if every message is a bot's, or if
  its name matches a feed keyword.
- **"report"/"reports" leave the name keywords.** A bot-driven report
  channel still qualifies through the bot share.

## Consequences

- Report channels written by people now render as regular digests: more
  text, bounded by the existing max_chars budget.
- Two tools-level tests reproduce the original miss and fail on the code
  before this change.

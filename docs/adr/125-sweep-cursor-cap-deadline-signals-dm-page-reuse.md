# ADR 125: cursor capped at the sweep start; fan-outs report the deadline; DM window reuses pages

Date: 2026-10-20
Status: accepted

## Context

The phase log from ADR 121/122 showed three things about the unread
sweep.

- **Fan-outs hid the deadline.** UnreadAll, the DM window, the priority
  channels and the own-thread backstop each loop over conversations or
  threads and tolerate one item failing: the error is logged and the loop
  goes on. When the sweep deadline passed mid-loop, every remaining item
  failed the same way and was logged as an ordinary failure, the phase
  returned no error, and the sweep tracker never marked it cut. The
  cursor then advanced past what the phase never read. Thread replies
  cut inside `Unread` were dropped the same way.
- **The cursor could pass messages it never saw.** The cursor is the
  newest timestamp the sweep saw. A sweep reads conversations one after
  another for up to its deadline, so a message can land in a
  conversation already read while a conversation read later supplies a
  newer timestamp. The next delta pull dropped the first message for
  good.
- **The DM window read every DM twice.** UnreadAll already reads the
  newest history page of every DM (it has a `last_read`). The DM window
  then read the same page again for every DM, at Slack's rate limit —
  the single slowest phase of the sweep.

Discovering active DMs through search (`is:dm`) was considered and
rejected: search indexes DMs with a lag, and a just-sent DM in a quiet
conversation is exactly what the get_mentions DM backstop exists to
catch.

## Decision

- **Fan-outs report the deadline.** Each loop stops at the deadline
  instead of logging every remaining item, keeps what it already read,
  and returns the context error. Callers merge the partial result and
  the tracker marks the phase cut, so the cursor holds. A deadline cut
  in PriorityActivity is no longer reported as a missing channel.
- **The cursor is capped at the sweep start** (`capCursor`). What
  arrived during the sweep is shown again by the next pull instead of
  being lost; a small repeat is the price of never dropping a message.
- **The DM window reuses the unread sweep's pages.** UnreadAll records
  the page it read for each DM (`historyPages`), reset at the start of
  every sweep and good for two minutes at most. `RecentDMActivityAfterUnread`
  lends a recorded page instead of reading again when it was requested
  with at least the window's limit. Both reads are anchored at now with
  no lower bound, so the first n messages of the larger page are the
  smaller page. A message arriving after the page was read is newer
  than the sweep start, and the capped cursor keeps it for the next pull.
- **get_mentions never reuses a page.** It has no capped cursor; its DM
  backstop calls `RecentDMActivity`, which always reads fresh.
- A `dm window` log line records how many pages were lent.

## Consequences

- A sweep cut by its deadline always says so and always holds its
  cursor, whichever phase was cut.
- Messages that arrive during a sweep can appear in two consecutive
  pulls.
- The DM window costs no history reads for DMs the unread sweep has
  read; it still reads thread replies for DM threads active in the
  window.

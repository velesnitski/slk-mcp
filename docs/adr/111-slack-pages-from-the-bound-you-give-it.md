# ADR 111: Slack pages from the bound you give it

Date: 2026-09-24
Status: accepted
Supersedes: the fetch strategy of ADR 106 (its intent stands)

## Context

`get_channel_digest` on a busy direct message returned `(no activity)`
for the last six hours. The conversation had dozens of messages from
that morning; search found every one of them. The digest returned
nothing and said so as if it were true.

ADR 106 had widened the history fetch to `oldest = window start - 7
days` so that a reply inside the window could find a thread parent
posted before it. The code carried the reasoning in a comment: Slack
returns one page newest-first, so moving `oldest` back "cannot push the
window's own messages off it". That premise was never measured.

Measured against the live API on the affected conversation, with
`limit=50`:

| request                    | page returned            |
|----------------------------|--------------------------|
| no bounds                  | the newest 50            |
| `oldest` = now − 6 h       | the newest 50 (window < one page) |
| `oldest` = now − 7 days    | **the oldest 50 of the week** — nothing from today |

With `oldest` set and `latest` unset, Slack returns the page adjacent
to `oldest`, not to now. Both pages are ordered newest-first, which is
why the assumption looked right in every quiet channel: when a range
fits on one page, both readings return the same messages. It fails only
once a range holds more than a page — a busy DM, an active channel over
a week — and then it fails silently, returning a full, well-formed page
of the wrong messages.

The same pattern sat in four more places: the multi-channel digest,
`find_decisions`, `export_conversations`, and the `post_message`
duplicate guard. The last is the sharpest: in a channel with more than
100 messages in half an hour the guard read the oldest 100 and could
miss the operator's own post from a minute ago — the duplicate it
exists to prevent.

Writing the regression test exposed a second, older defect in the
renderer. Replies to threads started before the window were rendered
only when the window held no top-level message. A window with both —
the normal case in an active channel — fetched and counted those
replies and never showed them.

### Why the tests did not catch it

Every history fake in the suite answered `conversations.history` with a
fixed body and ignored `oldest`, `latest` and `limit`. No test could
observe which slice of a conversation a request returns, so code built
on a wrong model of paging passed all of them — and five tests went
further and pinned the wrong model, asserting that `oldest` was sent.
Test data was also small enough to fit on one page, the only case where
the wrong model gives the right answer. Coverage was high; it measured
which lines ran, not whether the fake behaved like Slack.

## Decision

A window is fetched from its upper edge and trimmed to its lower edge
locally. `recentHistory` (`internal/tools/history.go`) is the one place
that does this; the five callers above go through it. A window larger
than one page loses its oldest part — the part a digest, a decision
scan and a duplicate check can best afford to lose.

Thread discovery keeps ADR 106's intent by a different route: a second
page, fetched downward from the window's lower edge
(`threadParentsBefore`), trimmed to the seven-day lookback, keeping only
parents whose newest reply landed in the window. It runs only when
replies were requested or the window has no top-level message, so the
common case still costs one call; a failure there is logged and costs
older thread replies, not the digest.

`ChannelDigest` renders replies whose parent is outside the window in a
block after the window's messages, in the same format the empty-window
branch already used.

`operatorRepliedSince` keeps its `oldest`-anchored page: it asks
whether the operator answered soon after a given message, and the page
adjacent to that message is the right one for that question.

The test suite gains `pgHistory`, a history fake that pages the way the
live API was measured to, and five regression tests on conversations
larger than one page. All five fail against the previous code.

## Consequences

- Busy conversations show their newest messages in every tool that
  reads a time window.
- A range larger than one page is truncated at its old end, not its new
  end. That is a behaviour change for `export_conversations`: a capped
  export now keeps the recent part, and repeated runs (deduplicated by
  corpus keys) fill in the rest.
- A digest with requested replies or an empty window costs a second
  `conversations.history` call.
- Any future history call that sets `oldest` without `latest` should
  be read as a bug unless it wants the page adjacent to `oldest`, and
  should say so in a comment.

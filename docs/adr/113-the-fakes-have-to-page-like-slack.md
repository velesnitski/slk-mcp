# ADR 113: the fakes have to page like Slack

Date: 2026-09-25
Status: accepted

## Context

ADR 111 found that `conversations.history` bounded by `oldest` returns
the page adjacent to `oldest`, and fixed the digest, decision scan,
export and duplicate guard. ADR 112 then found the same defect in the
DM window. An audit of every remaining history and replies call found
three more, all in the core unread sweep and thread reading:

- **`Unread()`**, the per-channel unread fetch, requested history with
  `oldest` set to `last_read`, pushed a further twelve hours back to
  catch already-read thread parents. In any busy channel the returned
  page was filled with messages from before `last_read`, the unread
  filter emptied it, and a channel with unread messages dropped out of
  `get_unread_summary`. The defect is in the tool's primary function.
- **`fetchReplies`** read one page of 100 thread replies and
  **`ThreadReplies`** one page of 200. `conversations.replies` returns a
  thread oldest-first, so the newest replies of any longer thread — the
  ones a digest exists to show — were never fetched.

The same question as in ADR 111 applies: why did the suite not catch
this? Every service-layer fake answered history and replies with a fixed
body and ignored `oldest`, `latest`, `limit` and `cursor`. A test could
only observe that a request was made, never which slice of the
conversation came back. Worse, two tests asserted the `oldest` parameter
itself — the implementation of the wrong model, written down as a
requirement. Test data was also small enough to fit on one page, the one
case in which the wrong model gives the right answer.

A third suspect from an earlier review — `search.messages` sending
`page=0` — was measured against the live API and is harmless: Slack
treats it as page 1. It is left as is.

## Decision

`Unread()` fetches the newest page (headroom unchanged) and applies
`last_read` locally; thread parents come from the part of that page
behind `last_read`, within the lookback. A channel whose unread backlog
exceeds the page shows its newest messages and loses its oldest.

Thread replies are read to the end with the cursor, bounded at ten pages
of 200. A thread past the bound keeps its first pages and logs that the
newest replies may be missing.

The `slack` package gains `svPager`, a fake that pages history and
replies the way the live API does. Four regression tests use it, with
conversations larger than one page; three fail against the previous
code, and the fourth guards that already-read thread parents are still
found. The two tests that asserted the `oldest` parameter now assert
the behaviour instead: no lower bound on the request, `last_read`
applied to the result.

## Consequences

- The unread sweep shows busy channels, with their newest unread
  messages first.
- Long threads show their newest replies in digests, `get_thread` and
  the unread sweep, at the cost of one call per 200 replies.
- History and replies are now faked faithfully in both packages
  (`pgHistory` in tools, `svPager` here). A new test of windowed reading
  should use them; a test that asserts request parameters instead of the
  returned slice is the pattern that let this class of defect through.
- Every `conversations.history` call that sets `oldest` without `latest`
  either wants the page adjacent to `oldest` and says so in a comment
  (`operatorRepliedSince`, search context), or is a defect.

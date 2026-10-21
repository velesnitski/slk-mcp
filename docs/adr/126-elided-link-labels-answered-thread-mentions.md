# ADR 126: elided link labels render the URL; answered thread mentions are dropped

Date: 2026-10-21
Status: accepted

## Context

Two defects surfaced in one sweep.

- **A shared document could not be opened from the output.** Slack
  stores a pasted link as `<url|label>`, where the label is its own
  display of the URL: the scheme dropped and, for a long URL, the middle
  elided with "…". `RenderText` rendered every `<url|label>` as the
  label, so a document link reached the reader with its id cut out —
  in every tool, `full_text` included — and the only way back to it was
  searching for the raw message.
- **A mention already answered was reported as pending.** The thread
  mention backstop (ADR 102) surfaces `to:me` and handle hits in threads
  whose parent is read. It never looked at whether the operator had
  replied in that thread since, so a colleague's question that had been
  answered minutes later still appeared as something to act on.

## Decision

- **`linkText`** renders `<url|label>`: when the label is the URL itself
  (with or without the scheme) or an elided form of it — the text before
  "…" is a prefix of the URL, the text after it a suffix — the URL is
  shown, scheme dropped. Any other label is real text and is kept, so
  ticket keys, "dashboard" links from bots and named links stay compact.
  Bare `<url>` is unchanged.
- **Answered thread mentions are dropped.** UnreadThreadMentions runs one
  more search, `from:me` over the same window, and keeps the operator's
  newest post per channel and thread root. A mention older than the
  operator's newest post in its thread is not shown. Only hits authored
  by the operator count, whatever the search returns. If that search
  fails, every mention is shown, as before.

## Consequences

- Pasted links in messages are readable and openable from any tool's
  output. Long ones cost the characters of the full URL.
- The thread mention backstop costs one more search call per sweep.
- A reply the search index has not caught up with yet does not count:
  the mention shows until it is indexed, never the other way round.

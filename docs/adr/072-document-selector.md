# ADR 072: pick a document, don't just take the newest

Date: 2026-08-10
Status: accepted

## Context

ADR 071 made attachments readable, and the first real use hit the next
wall immediately. A colleague posted two documents three minutes apart —
a playbook, then a proposal. `read_document` in latest-mode returned the
proposal and there was no way to reach the playbook without first
hunting down its message timestamp by hand, which is exactly the manual
step these tools exist to remove.

"The newest attachment" is a good default and a bad only-option. The
same shape already exists for canvases (ADR 060: `date` / `match` /
`list_only`), so the fix is to bring `read_document` in line rather than
invent a second idiom.

## Decision

**Slack layer.** New `RecentFileMessages(channelID, accept, fromUserID,
limit)` returns up to `limit` messages carrying accepted attachments,
newest first. It scans the top level *and* recent threads (ADR 070), so
a document posted as a reply is a first-class candidate, then
de-duplicates by timestamp — `conversations.replies` repeats the parent
that `conversations.history` already returned — and sorts newest-first.
`LatestFileMessage` keeps its short-circuit and is untouched: the common
case still costs one history call.

**Tool layer.** `read_document` gains `match` (case-insensitive
filename substring), `limit` (default 1), and `list_only`. Selector mode
engages only when one of them is set and neither a permalink nor a
timestamp was given — naming an exact message still wins, as it should.
A `match` that hits nothing returns an error that names the needle and
points at `list_only`, instead of silently falling back to the newest
document, which would be the same bug wearing a hat.

`list_only` prints each candidate's name, mimetype, size and message
`ts`, so the follow-up call can address it exactly.

## Consequences

- Two documents posted together are both reachable, by name or by
  listing, with no timestamp archaeology.
- Selector mode costs more than latest-mode: one history call plus up to
  12 `conversations.replies`. It runs only when explicitly requested.
- `docScanLimit = 25` bounds how many document-bearing messages are
  considered, so a typo'd `match` cannot walk the whole history.
- `MessageClient` grew a method, so every fake implements it. That is
  the price of the narrow-contract pattern and worth paying.
- 614 → 618 tests. Minor release (1.29.0).

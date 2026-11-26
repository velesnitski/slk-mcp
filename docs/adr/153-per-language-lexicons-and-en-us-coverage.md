# 153. Per-language lexicons and full en-US coverage

Date: 2026-11-27

## Status

Accepted

## Context

get_my_actions matched commitments with mixed Russian/English word lists
inside detect.go. The Russian side had grown from daily use; the English
side covered only the basics. Common US phrasing was missed: follow-up
nudges ("any update?", "ETA?", "gentle reminder"), soft asks ("do you
mind", "when you get a chance"), completion words ("finished", "all set",
"LGTM"), acks ("sounds good"), time off ("OOO"), urgency ("P0",
"blocker") and same-day deadlines ("by COB", "by 5pm"). A nudge to the
operator did not surface, and a "finished" reply did not close an item.

## Decision

- Each language is one table (`lexicon_ru.go`, `lexicon_en.go`) with the
  same categories: promise, request, done, ack, absence, thanks, urgent,
  polite, redirect, self. The detector compiles the union of all tables.
  Languages are always all on: workspaces and single messages mix them,
  so a per-workspace language switch would miss half of them.
- The en-US table fills the gaps above. Deadline phrases that are not
  word lists stay in detect.go: COB/EOB/close of business, "by noon",
  "by 5pm" (same day, strong); "this afternoon", "later today" (weak);
  "first thing in the morning" (+1, weak); "in a couple of days" (+2,
  strong); "in a few days" (+3, weak); a weekday with a part of the day
  ("Thursday morning" weak, "Friday EOD" strong). A relative phrase inside
  a weekday-part phrase belongs to the weekday: "Friday EOD" is Friday.
- Status asks ("any update", "ETA", "keep me posted", "where are we on")
  are settled by an answer, like "let me know".
- lexicon_test.go holds golden phrases per category for en-US, and
  requires every table to fill every category.

## Consequences

- Adding a language is one table plus its golden phrases.
- Broader English words ("updated", "approved", "critical") are judged in
  context as before: done words only close an existing item; an urgency
  word alone no longer makes an ask (ADR 150).

# 152. Date order, ordinal dates and redirects

Date: 2026-11-27

## Status

Accepted

## Context

- get_my_actions read every numeric date day-first. In a US workspace
  "by 12/3" means December 3; it was filed as 12 March, a wrong deadline
  with no sign of the misreading.
- English ordinals ("Dec 3rd", "the 22nd of November") set no deadline:
  the patterns expected a bare number.
- An outgoing message that only sends someone elsewhere ("по доступам
  обратись pls к devops", "ask the ops team") was listed as waiting on a
  reply after stale_hours. It owes the operator nothing.

## Decision

- `SLACK_DATE_ORDER`: `dmy` (default, unchanged behaviour) or `mdy`;
  `en-US`/`us` are accepted as `mdy`. Each `SLACK_WORKSPACES` entry may
  set `date_order`, so a US workspace and a European one can be read
  side by side. An unknown value fails `Validate` instead of being
  silently ignored.
- Month-first applies to slash dates only. A dotted date (12.11) is the
  European form and stays day-first in every workspace.
- A date that is impossible in the configured order but valid in the
  other (12/25 day-first, 25/12 month-first) is read the valid way: it is
  not ambiguous.
- The same setting prints dates: `Fri 27.11` / `Fri 11/27`, and
  `27.11 15:04` / `11/27 15:04`. Times stay 24-hour.
- Day numbers may carry st/nd/rd/th, the month may carry a dot, and
  "the" / "of" are allowed ("until the 22nd of November").
- The operator's request is not Waiting when, after removing the
  redirect phrase and politeness words, no request word is left and the
  message asks no question. A redirect to the operator ("ask me",
  "обратись ко мне") is not a redirect.

## Consequences

- `actions.Due`/`DueOfRequest` keep day-first; `DueIn`/`DueOfRequestIn`
  take the order. Existing callers and tests are unchanged.
- Redirect detection is phrase-based like the rest of the detector; an
  unusual wording can still be listed.

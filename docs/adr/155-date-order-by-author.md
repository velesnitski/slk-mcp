# 155. Date order by author

Date: 2026-11-27

## Status

Accepted

## Context

ADR 152 and 154 set the date order per workspace. In a workspace where
US and European people write side by side, one setting reads half of
"by 12/3" wrong. Slack's users.list returns each person's UI locale and
time zone, but the locale alone is unreliable: Slack defaults to en-US,
and many people who write 27.11 never change it.

## Decision

- get_my_actions reads users.list once per workspace (the same read
  that finds bot users) and derives each author's date order:
  - month-first when the locale is month-first (en-US, es-US, en-PH)
    AND the time zone is one where that convention is used: the US
    zones, US territories, or Asia/Manila;
  - day-first when the locale is any other region-qualified locale:
    choosing en-GB, ru-RU or uk-UA is deliberate;
  - unknown otherwise (en-US outside the US, no locale, a bare "en"):
    the workspace order applies.
- Each message's numeric dates are read in its author's order; the
  output still prints dates in the workspace's order, for the reader.
- `SLACK_AUTHOR_DATE_ORDER` (default true) turns it off.
- A failed users.list read falls back to the workspace order for all.

## Consequences

- Teams whose members kept Slack's default locale see no change.
- Only numeric slash dates depend on this; dotted dates, month names
  and weekdays never did.

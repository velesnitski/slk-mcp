# 154. Locale defaults and a 12-hour clock

Date: 2026-11-27

## Status

Accepted

## Context

ADR 152 added `SLACK_DATE_ORDER`; times stayed 24-hour everywhere. A US
user needs two settings to get US formats, and has no way to get
`3:04 PM`. Reading the machine's locale would be one setting fewer, but
`LANG=en_US.UTF-8` is the default on many machines whose owners write
`27.11` and `15:04`: switching them silently would misread "by 12/3" in
their own messages.

## Decision

- `SLACK_LOCALE` (a language with an optional region; `en_US.UTF-8`
  accepted) supplies defaults only:
  - month-first: en-US, es-US, en-PH; everything else day-first;
  - 12-hour: en-US, es-US, en-PH, en-CA, en-AU, en-NZ, en-IN, hi-IN;
    everything else 24-hour.
- `SLACK_DATE_ORDER` and `SLACK_CLOCK` (`12h`/`24h`) override each part.
  A workspace entry may set `locale` for its date order; the clock is one
  setting for the whole server.
- The machine's `LANG`/`LC_*` are never read. Nothing changes unless a
  setting is given.
- `format.Clock` renders every time of day; digest days (`format.Day`)
  follow the primary workspace's order, get_my_actions each workspace's.
  ISO timestamps (`2006-01-02 15:04`, used for scheduled messages and
  exports) stay as they are: they are unambiguous and machine-read.
- Invalid values fail `Validate` at start.

## Consequences

- A US setup is one line: `SLACK_LOCALE=en-US`.
- Locale tables are short and curated; an unlisted locale gets day-first
  and 24-hour, and the two explicit settings cover anything else.

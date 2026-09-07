# ADR 095: rendered times name their zone

Date: 2026-09-07
Status: accepted

## Context

Slack timestamps are epoch seconds. This server renders them in the
host's local zone, which is the right default: the operator reads them
against their own clock.

But the zone is invisible in a bare `15:04`. The moment those times are
copied into anything that also contains machine timestamps — an
incident timeline, a report quoting log lines, a message from a vendor
in another country — the reader has to know the offset to line them up,
and nothing in the output tells them.

The failure mode is quiet. Nobody notices a missing zone; they notice
an interval that is three hours wrong, usually after it has been
written down somewhere and read by someone else. Reconstructing which
of two sources was shifted costs far more than printing the answer
would have.

## Decision

`format.TimeZoneNote()` returns the zone abbreviation and signed offset
— `times: XXX (UTC+HH:MM)` — and the unread-summary header carries it.

Once per report, not once per line: the cost is a few tokens on a
surface that is already a header, and every clock time under it is
covered by the same statement.

## Consequences

- Times rendered here can be correlated with UTC sources without
  guessing.
- The note reflects the host's zone, so it stays correct if the server
  is run somewhere else.
- Other surfaces can adopt the same helper; the summary header is where
  the mixing actually happens.

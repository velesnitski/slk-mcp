# ADR 098: the digest says which days it covers

Date: 2026-09-11
Status: accepted

## Context

`get_channel_digest` takes `after`/`before` as `YYYY-MM-DD`.
`parseRange` sets the upper cutoff to `before + 24h`, so the day named
in `before` is fully included.

Both descriptions of that behaviour were wrong, and wrong in opposite
directions. The tool description said `before` was an "exclusive day
end". The doc comment said `after=2026-04-30 before=2026-05-01`
returned "one full UTC day" — that range returns two.

A caller who believes either one asks for a window and silently gets a
different one. Reading `before=2026-08-25` as exclusive and receiving
the 25th's messages is not a visible error: the digest looks correct,
it is just answering a question that was not asked, and the extra day
is indistinguishable from the requested ones.

## Decision

Keep the behaviour, fix both descriptions.

The named day is included at both ends. `after=2026-04-30
before=2026-04-30` is that one day, and that is the documented way to
ask for a single day. `before=2026-05-01` returns May 1st.

Inclusive is the reading the parameter names invite for a month or a
sprint (`after=2026-09-01 before=2026-09-30`), and it is what the code
has always done — so this is a documentation change, not a silent shift
of everyone's existing windows by a day.

A test now pins the single-day case, which also guards the
inverted-range check against rejecting `after == before`.

## Consequences

- No behaviour change; no caller's window moves.
- The `+24h` in `parseRange` is now explained where it is written,
  rather than contradicted.

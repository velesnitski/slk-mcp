# ADR 084: a channel miss suggests near matches

Date: 2026-09-01
Status: accepted

## Context

`ResolveID` walks the whole workspace listing to find a channel by name,
caching every name it passes on the way. When nothing matches it returns
`channel #x not found` — having just seen, and cached, every channel that
does exist.

The realistic miss is not a typo, it is a **shorthand**. People say
"#orbit-relay" for a channel actually named "#orbit-relay-monitoring",
"#devops" for one of four channels starting that way. Each such miss cost
a full round trip: the caller had to run a separate channel search to
discover a name the failing call already had in hand.

## Decision

On a miss, rank the cached names and name the closest few in the error:

    channel #orbit-relay not found — did you mean #orbit-relay-alerts,
    #orbit-relay-monitoring?

Ranking is ordered by how people actually miss:

1. **Known name starts with the input** — the shorthand case, ranked first.
2. **Input starts with a known name** — the caller typed more than the
   channel is called.
3. **Either contains the other.**
4. **Edit distance ≤ 2** — a slip of the finger, last because it is the
   least likely and the most prone to wild guesses.

Within a tier the shortest name wins: it is the closest fit to what was
typed. An exact match is never suggested — reaching the miss path with
one means something else is wrong, and echoing the input back is noise.

Distance is computed with a bounded two-row Levenshtein that abandons a
row once every cell exceeds the budget. A full matrix against every
channel in a large workspace is wasted work when anything past two edits
would not be offered anyway.

## Consequences

- A wrong channel name costs one call instead of two, and the correction
  arrives in the error rather than requiring a search.
- Nothing extra is fetched: the suggestions come from the cache the
  failed walk just populated.
- Unrelated input still gets a bare "not found" — the tiers are narrow
  enough that a name sharing nothing with any channel produces no
  suggestions, which keeps the error honest.
- Ranking and distance are pure and unit-tested with no API in the loop.

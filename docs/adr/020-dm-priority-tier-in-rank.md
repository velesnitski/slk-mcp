# ADR 020 — DMs get a priority tier in the unread ranker

**Status:** accepted
**Date:** 2026-06-01
**Tag at acceptance:** v0.4.18

## Context

`get_unread_summary` emits channels in `RankUnread` order and stops
inlining once `max_chars` is reached; the rest go to a
"N channels omitted" footer. The rank had two effective tiers:

1. mention (`+1_000_000`)
2. urgency + raw volume (everything else, realistically < ~100k)

A 1:1 DM that did not contain an explicit `<@selfID>` mention sat in
tier 2, ranked purely on volume + urgency. A busy bot/log channel
(e.g. a monitoring feed with 200 messages, many carrying urgency
keywords like "error"/"failed") could therefore outrank a quiet,
high-signal personal DM and push it below the `max_chars` cap.

Observed live: a substantive 1:1 DM was truncated into the
omitted-channels footer while log feeds were inlined. The content
existed but didn't surface, and a one-line DM in the footer carries
no context at all. A 1:1 DM is categorically higher-signal than a
bot log feed; the ranker didn't reflect that.

## Decision

Add a **DM tier** between mention and urgency/volume:

```
mentionBonus = 1_000_000   // explicit <@selfID> — top tier
dmBonus      =   500_000   // 1:1 or mpdm — above every non-mention channel
                           // urgency + volume — the rest (< ~100k realistically)
```

`RankUnread` now adds `dmBonus` when `slack.IsDirectMessage(cu.Channel)`
is true. Consequences of the tier arithmetic:

- A plain DM (`500k`) outranks any non-mention channel, so it is
  inlined before log/git feeds and survives the cap.
- An explicit mention (`1M`) still outranks a plain DM — a real ping,
  even in a log channel, is the highest signal.
- A DM that also mentions you stacks both (`1.5M`) and sits at the
  very top.
- The gap to the urgency/volume band is wide (≥ 5×) so no realistic
  volume or keyword spam can promote a non-mention channel into a
  tier.

### Reusing the v0.4.12 DM detector

Detection goes through the **exported** `slack.IsDirectMessage`
(promoted from the unexported `isDirectMessage` introduced in
ADR 014 / v0.4.12). That detector falls back to the channel-ID
prefix (`D…`, or `G…` + `mpdm-` name) when Slack omits the
`IsIM`/`IsMpIM` flags on stale-listing DMs. Reusing it matters: the
flag-missing DMs are *exactly* the ones most likely to be
volume-ranked low and dropped, so the tier must apply to them too. A
naive `IsIM || IsMpIM` check here would have missed the worst case.

## Consequences

- **DMs no longer fall off the digest behind noisy channels.** The
  reported failure cannot recur regardless of `max_chars` value.
- **No new tool surface, no API change.** Pure ranking adjustment;
  `get_unread_summary` parameters are unchanged.
- **Slight reordering of existing output:** DMs now cluster directly
  below mentions, ahead of high-volume channels. This is the
  intended behaviour change; consumers that expected strict
  volume ordering among non-mention items will see DMs lifted.
- **`slack.IsDirectMessage` is now part of the package's exported
  surface.** Single source of truth for DM detection across the
  unread sweep (ADR 014) and the ranker (this ADR).

## Validation

- `go vet ./...` — clean.
- `go test -race -count=1 ./...` — green.
- 4 new ranker tests: DM outranks a 200-message keyword-heavy
  channel; mention still outranks a plain DM; DM+mention tops both a
  DM-only and a mention-only channel; a flag-missing `D…`-prefix DM
  still receives the tier.

## Out of scope

- A per-DM-importance heuristic (some DMs matter more than others).
  The tier is binary; refining within the DM band is unjustified
  until there is evidence the binary split is too coarse.
- Changing `get_mentions` to auto-attach context lines for DM hits
  (the *other* improvement discussed alongside this one). Tracked
  separately; this ADR covers only the ranker tier.

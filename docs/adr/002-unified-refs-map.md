# ADR 002 — Unified `id→name` refs map for users and channels

**Status:** accepted
**Date:** 2026-05-12
**Tag at acceptance:** v0.3.26

## Context

`format.RenderText` substitutes readable names for Slack-flavoured
markup inside message bodies — `<@USERID>` → `@Alice`, link markup
→ label. Before v0.3.26 it only handled user mentions; channel
references (`<#CHANNELID>` and `<#CHANNELID|name>`) were rendered
verbatim, so a backend asking "сможем прогнать МРы из
`<#C0XYZ12345>`?" came out with the opaque ID visible.

Adding channel resolution required threading a second lookup table
into the rendering pipeline alongside the existing `users` map. The
shape of the second parameter was the open question.

### Options considered

- **a.** Add a second `channels map[string]string` parameter to
  `RenderText` and every transitive caller (`MessageLine`,
  `ChannelDigest`, `LogChannelDigest`, …). Source-compatible in
  spirit (callers can pass `nil`) but requires editing every
  signature and every call site.
- **b.** Pass a single map keyed by Slack ID for **both** user and
  channel display names. Safe because Slack guarantees disjoint
  ID-prefix namespaces: users `U`/`W`, channels `C`/`G`, DMs `D`.
  No identifier can mean both "user X" and "channel Y" at once.
- **c.** Introduce a `Refs` type (a named `map[string]string` with
  helper methods). Type-stronger than (b) but otherwise identical;
  more churn for the same payoff.

## Decision

Use **(b)**. `RenderText`'s second parameter is now semantically a
**merged id→name map**. Callers in `internal/tools/` populate it via
`(h *Hub).resolveRefs` (and the reply-aware
`resolveRefsWithReplies` for the unread path), which:

1. Resolves user IDs via `slack.UserService.NamesFor`.
2. Resolves channel IDs via `slack.ChannelService.NamesForIDs`
   (a new batch helper with a reverse `idCache`).
3. Merges into a single map (`mergeRefs`).

The function-parameter NAME stays `users` in places where it
predates this ADR; it's renamed gradually as those files are
touched. Documentation everywhere now spells out the disjoint-prefix
guarantee.

## Consequences

- Channel references resolve to readable `#name` everywhere
  digests, search results, and threads are rendered.
- No signature explosion across `format/`; one parameter handles both
  reference kinds.
- The map's caller is now responsible for pre-resolving channel
  names too. Forgetting to do so silently falls back to the
  last-resort `#CID` placeholder — a quiet failure mode. We
  mitigate this by routing all production callers through the
  central `Hub.resolveRefs` helper.
- New behavioural contract: the rendering layer relies on Slack's
  ID-prefix uniqueness rules. If Slack ever introduces a fourth
  prefix that collides, this assumption breaks. (No evidence of
  that happening; the rules have been stable for >5 years.)

## Validation

`TestRenderText_ResolvesChannelRefs` in
`internal/format/format_test.go` covers:
- inline pipe label wins (`<#CID|name>` → `#name`);
- bare `<#CID>` resolves from the map;
- mixed bodies with user + channel refs;
- unknown ID falls back to `#CID` (never silently dropped).

`TestCollectMentionedChannelIDs_DedupesAndIgnoresInvalid` validates
the collector that builds the map's input.

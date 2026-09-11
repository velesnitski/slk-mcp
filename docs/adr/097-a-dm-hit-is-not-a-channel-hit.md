# ADR 097: a DM hit is not a channel hit

Date: 2026-09-11
Status: accepted

## Context

`search_messages` rendered every hit as `- #<channel.name> <when>
(<author>) <body>`.

That holds for a public or private channel. It does not hold for a
direct message: Slack returns no channel name for one, and parks the
counterpart's user ID in the `name` field instead. So a DM hit came
back labelled `#U…` — a channel that does not exist, named after a raw
user ID, rendered with the same glyph as a real channel.

Two failures follow from one line. The reader cannot tell which
conversation the hit came from without a second call, and a search that
sweeps channels and DMs together presents the DM results as if they
were channel results. When the question being asked is "where was this
said", answering it with a fake channel name is worse than answering it
with nothing.

## Decision

Name the conversation by what it is. `SearchChannelLabel` reads the
conversation ID — `D…` for a DM, `IsMPIM` for a group DM — and renders
those behind `@` rather than `#`.

The handler resolves the parked IDs through the existing user cache in
one batched call and hands the map to the renderer, so a DM hit reads
`@handle`. When resolution fails the label degrades to `@U…`: still
honest about being a DM, still traceable through `list_users`.

The rendering stays a pure function of the hit plus that map, so both
halves are covered without a live search.

## Consequences

- A mixed sweep is readable in one pass: `#channel` and `@person` are
  distinguishable at a glance.
- One extra `users.info` batch per search, served from the cache for
  anyone already seen this session.
- `SearchResultExt` takes a third argument. `SearchResult` keeps its
  signature and passes `nil`, which degrades to the bare ID.

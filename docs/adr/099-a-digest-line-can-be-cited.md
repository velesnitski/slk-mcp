# ADR 099: a digest line can be cited

Date: 2026-09-14
Status: accepted

## Context

`get_channel_digest` renders each message as a timestamped line with an
author and a body. It carries no key: not the message `ts`, not a
permalink, nothing that addresses the message it just printed.

So a reader can see a message and cannot point at it. Re-fetching it
means searching its text back out of the workspace, which fails exactly
when it matters most: the body was truncated, or the phrase is not
distinctive, or the message is one of several near-identical lines.

The failure this closed was worse than an inconvenience. Asked to cite
one message from a digest, the only handle available was the channel
reference — so a plausible-looking permalink got assembled out of it by
hand. It pointed at the wrong conversation and at no message, and
nothing in the rendering made that detectable. A renderer that shows
data with no key invites its consumer to invent one.

`SearchResultExt` already got this right (ADR 097): every hit carries
`thread_ts` and a real permalink, which is why search results can be
chained into `get_thread` and digests cannot be chained into anything.

## Decision

`WithMessageTimestamps` appends `ts=<ts>` to each top-level digest
line, exposed as `with_ts` on `get_channel_digest`.

The `ts` rather than a permalink: it is what `get_message` accepts
alongside the channel, it is half the width, and it does not require
the renderer to know the workspace host — a permalink built without
that is a guess, which is the failure being closed.

Off by default. The suffix costs about eighteen characters a line,
which is free in a single-channel read and not free across a wide
sweep; the caller who intends to cite something asks for it.

The same change fixes the heading: `"#"+ref` decorated whatever it was
handed, so `#devops` rendered `##devops` and the DM `@person` rendered
`#@person`. `conversationLabel` gives a channel exactly one `#`, keeps
a DM's `@`, and leaves a bare conversation id undecorated — a `D…` id
is neither a channel nor a handle, and asserting either about it is the
same error one level down.

## Consequences

- A digest can be chained into `get_message` without a second search.
- Default output is unchanged, byte for byte.
- Headings name what they render. `##devops` was visible in every
  digest this tool has ever produced and had gone unremarked.

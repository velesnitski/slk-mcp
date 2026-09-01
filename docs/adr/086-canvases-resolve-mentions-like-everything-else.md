# ADR 086: canvases resolve mentions like every other read surface

Date: 2026-09-01
Status: accepted

## Context

`read_canvas` was the only read surface that did not resolve mentions.
Digests, messages, threads, search and unread all run bodies through
`format.RenderText` with a resolved ref map; the canvas path returned
whatever came out of the HTML flattener.

The result was worse than unreadable, because it contradicted a signal
the same server emits. The unread summary flags a canvas as *mentioning
you*; opening it then showed a wall of raw IDs, so the reader was told
they had been named and could not find where. A guard that says "look
here" and then hides the thing is worse than one that says nothing.

There is a second reason it went unnoticed: **canvases lose Slack's
markup before anyone can render it.** They download as HTML, and the
flattener strips tags — which takes the angle brackets off `<@U…>` with
them. By the time the text is readable, `mentionRefRe` has nothing to
match, so simply calling `RenderText` here would have resolved nothing
and looked like it worked.

## Decision

**`format.CollectRefIDsInText`** scans a plain string for user and
channel IDs in both spellings — the bracketed markup, and the bare
`@U…` / `#C…` left after flattening — deduped, first-appearance order.

**`format.RenderCanvasText`** is `RenderText` plus a bare-mention pass.
Kept as a separate function rather than folded into `RenderText`: that
one runs on every message on every surface, and widening its match is a
behaviour change nobody asked for. Canvases are the surface that lost
its brackets, so canvases get the wider renderer.

**`Hub.resolveTextRefs`** is `resolveRefs` for a body that is not a
message, splitting IDs by Slack's prefix rule (U/W are people, C/G/D are
conversations) so each is asked of the right service.

An ID that does not resolve is left exactly as it is. A guessed name
reads as fact and is worse than a raw ID.

## Consequences

- A canvas now reads like every other surface, and the "mentions you"
  flag can be acted on.
- The bare-mention regex requires a Slack-shaped ID (prefix letter plus
  at least seven uppercase alphanumerics), so ordinary prose — `@bob`,
  `#release-notes`, `#123` — is untouched. Tested.
- Message rendering is provably unchanged: a test asserts `RenderText`
  still ignores bare mentions.
- One extra name lookup per canvas read, batched, against services that
  already cache.

## Known gap, not addressed here

The unread summary reports that a canvas mentions you, but not *where*.
Canvases accumulate — a document can hold several meetings' notes — so
the mention that triggers the flag may sit in a section weeks old while
the recent edit is unrelated. The flag is true and still misleads about
recency. Worth fixing when it costs more than it does today.

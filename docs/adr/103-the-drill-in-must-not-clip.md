# ADR 103: the drill-in must not clip

Date: 2026-09-17
Status: accepted

## Context

`get_message` describes itself as fetching one message "verbatim — full
text, no truncation" and names itself the drill-in for any "(+N chars)"
preview a digest or sweep produced. That is the whole reason it exists
as a separate tool.

It did not do that for a message whose `text` field is empty. ADR 100
taught it to lift the body out of the non-text payload, but it did so
through the digest's renderer, which caps the lifted text at
`HiddenPayloadLimit` runes. So the tool whose job is to reveal a clipped
body clipped it again, and reported the remainder as "(+N chars)" with
no deeper drill-in to offer — the chain simply ended there.

This is worst exactly where it matters most. A forwarded message carries
its entire body in an attachment and none in `text`. Slack's own
attachment model gives the forward no route back to the original, so
when the original sits in a conversation the reader cannot open, the
payload on the forward is the only reachable copy of that text. Clipping
it made a message that was fully present in the API response
unreadable through this server.

A second defect sat in the same code. Both truncation sites cut the
body with a byte-index slice, `body[:limit]`. Go strings are UTF-8, so
that splits the final multi-byte rune and the reader gets a replacement
glyph instead of a letter — on any non-ASCII text, which here is most
of it. The same arithmetic made the "(+N chars)" count bytes while
saying chars, overstating the remainder by roughly the encoding's
bytes-per-rune.

## Decision

`renderHiddenPayloadMarker` takes a rune limit; `limit <= 0` renders the
payload whole. `HiddenPayload`, which only `get_message` calls, passes
0. The digest call site keeps `HiddenPayloadLimit`, so a verbose bot
attachment still cannot dominate a digest line.

`truncateRunes` is the one truncation primitive: it cuts on a rune
boundary and reports the remainder in runes. Both the payload renderer
and the message-line renderer use it, so no rendered line can end in a
split rune and every "(+N chars)" means characters.

## Consequences

- A truncated preview now always has a drill-in that terminates: the
  digest clips, `get_message` shows the rest.
- A forward whose original is unreachable is readable through the
  forward itself.
- Rendered text is valid UTF-8 at every truncation point.
- `get_message` on a bot message with a long attachment returns more
  than it used to; that is the documented contract, and the digest is
  still where bounded output lives.

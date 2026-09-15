# ADR 100: an empty text is not an empty message

Date: 2026-09-15
Status: accepted

## Context

A Slack message whose `text` field is empty is routinely not empty. Bot
notices put everything in attachments. A forwarded message puts the
original's author, timestamp and fallback line there too.

Four tools read the same such message and gave four answers:

- `get_channel_digest` rendered the attachment prose — correct, this is
  what ADR 092 fixed.
- `get_message` reported `chars: 0` and nothing else.
- `view_image` and `read_document` reported "this message has no
  matching attachment, and neither does any reply in its thread".

The digest was right and the other three were wrong about the same
bytes. Worse, the file tools were wrong in a way that misdirects: the
user is looking at a visible file preview in Slack while the tool
insists nothing is attached, so the natural conclusion is that the
download path is broken. It is not. The message really has no file —
the file belongs to the message it forwards.

That distinction cannot be closed by reading harder. `goslack.Attachment`
exposes `Fallback`, `Text`, `Blocks`, `Ts` and author fields, and **no
`Files` field at all**. A file that arrived through a forward is not
reachable from the forwarding message through this library by any
amount of digging.

## Decision

Stop reporting absence where the truthful answer is redirection.

`ForwardedOrigin` (and its local twin `forwardedOriginTS` in the tools
layer, which already imports the Slack types) recognises the shape: no
files of its own, an attachment carrying a timestamp. When `fetchFiles`
would otherwise fail, it now names the original's timestamp and the one
route that does work — the direct file URL, which the existing
`files.info` fast path already resolves.

`get_message` renders the attachment payload when `text` is empty, so it
agrees with the digest instead of contradicting it, and flags a forward
explicitly.

The duplication between `format.ForwardedOrigin` and
`forwardedOriginTS` is deliberate. The rendering layer and the fetching
layer each need the predicate; coupling them for eight lines buys
nothing and drags the file tools into a dependency on the renderer.

## Consequences

- A forward produces an actionable error naming the original, not a
  denial that anything was attached.
- `get_message` no longer reports a bot notice or a forward as blank.
- The file itself still cannot be fetched through a forward. This ADR
  closes the misdiagnosis, not the limitation; lifting it means
  resolving the original message, and Slack does not give us its
  channel through the typed attachment.

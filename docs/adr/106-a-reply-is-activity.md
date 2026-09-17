# ADR 106: a reply is activity, even when its parent is not

Date: 2026-09-17
Status: accepted

## Context

`get_channel_digest` returned a blank result for a channel that was
plainly active. Not "no messages" — nothing at all.

Two independent defects produced it.

**The reply was unreachable.** `conversations.history` returns only
top-level messages. The tool fetched the window, then looked for thread
parents *in that fetched page* to decide which threads to expand. A
reply posted inside the window to a thread started before it therefore
had no anchor: its parent was off the page, and `conversations.replies`
cannot be called without a root timestamp. So the reply was invisible at
any `hours` setting that did not happen to reach back far enough to
catch the parent — and widening the window to find it is exactly what a
caller asking for "the last four hours" should not have to do.

This is the same shape as the defects fixed earlier in this series: the
summary sweep reported "1 reply in earlier threads" for the channel, and
the drill-in the summary points at could not show it. The two tools
disagreed about the same channel, and the more specific one was wrong.

Notably the renderer already handled this case — `renderOrphanReplies`
exists, with a comment explaining that the parent may be older than the
window. The data simply never reached it.

**The blank.** With nothing to render, `ChannelDigest` returned `""`.
An empty string reaches the caller as an empty tool result, which is
indistinguishable from a failed call: the reader cannot tell "nothing
happened here" from "the tool broke". The rationale on record was token
efficiency, which is real — but it belongs to sweeps across many
channels, not to a call naming one channel on purpose.

## Decision

Thread discovery reaches back past the window. History is fetched from
`window start − 7 days`, and only messages inside the window are
rendered; the rest of the page exists to find parents. Slack returns one
page newest-first, so moving the lower bound back cannot push the
window's own messages off it — the call count and the per-channel cap
are unchanged.

`latest_reply`, already present on each parent, decides which threads
could have moved inside the window, so the widened range costs no extra
`conversations.replies` call for stale threads. Replies are then
filtered to the window: a revived thread carries its whole history, and
only the new part belongs in a windowed digest. A missing or unparseable
`latest_reply` fails open — dropping a live thread because a field was
absent is the failure this change exists to remove.

When the window holds no top-level message and replies were not
requested, the digest says how many threads received replies instead of
returning nothing. That count is free: it comes off the page already
fetched.

`ChannelDigest` returns `"## <channel>\n(no activity)"` rather than `""`
when it has nothing. Token efficiency becomes entirely `WithOmitEmpty`'s
job, which is what the multi-channel sweep passes; the per-channel
digest and the explicit multi-channel tool both now state the quiet case
plainly, matching how that path already reports per-channel errors.

## Consequences

- A channel whose only recent activity is a reply in an older thread
  reads correctly, with or without `with_replies`.
- An empty result now always means the tool ran and found nothing, and
  says so. A truly blank response again means something went wrong.
- Sweeps are unchanged: they pass `WithOmitEmpty` and still drop quiet
  channels.
- Thread discovery is bounded at a week. A thread revived after longer
  than that still needs a wider explicit window.

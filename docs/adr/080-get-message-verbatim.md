# ADR 080: get_message renders one message verbatim

Date: 2026-08-25
Status: accepted

## Context

Every read surface truncates by design: the sweep previews channel
messages at 280 chars, digests carry a max_chars budget, DMs cap at a
generous but finite limit. That is correct where many messages share one
result (ADR 026) — and it leaves a real gap, reported by the tool's
second user: there was no way to read a single long post in full. The
existing escapes (`full_text` on a channel digest) fetch a whole
channel to read one message, and still compete with the host's own
result-size ceiling.

## Decision

Add `get_message`: exactly one message, rendered verbatim.

- **No truncation, structurally.** Because the tool renders one message,
  the reason every other surface truncates does not apply. The one
  exception is deliberate: when the target is a thread reply, its
  PARENT is shown as a single capped context line — the parent is
  context, not the target.
- **A permalink is the primary input**, and its host routes the call:
  each configured workspace already self-identifies via the cached
  auth.test URL (`TeamURL`, ADR 077), so a pasted link lands in the
  right workspace without the caller knowing which one that is. An
  explicit `workspace` argument overrides; no match falls back to the
  primary and says so, so a wrong guess fails loudly in the channel
  lookup rather than silently reading the wrong workspace.
- Thread replies resolve through the permalink's own `thread_ts` —
  fetched from their thread, not guessed from channel history.
- Metadata that a preview drops is restored: absolute timestamp, edited
  flag, reactions with counts, attached files, thread position, and a
  rune count so the caller can tell at a glance that nothing was cut.

## Consequences

- The "(+N chars)" preview in any digest now has a one-call drill-in;
  the tool descriptions cross-reference it.
- Bot-only workspaces cannot self-identify (TeamURL needs a user token)
  and are skipped during host routing.
- Rendering, host matching, and the preview cap are pure and
  unit-tested without any API.

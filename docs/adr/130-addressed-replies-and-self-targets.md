# ADR 130: Addressed replies render in full; you-global changes follow the sweep

Date: 2026-10-29
Status: accepted

## Context

Three gaps showed up in daily use.

1. The unread sweep cut every thread reply to the 280-char channel
   preview. The replies the own-thread and mention backstops bring in are
   the ones addressed to the operator — an answer in their own thread —
   and the cut ("(+288 chars)") forced a second read of the same thread to
   see the answer.
2. `set_status`, `set_presence` and `set_dnd` with no workspace applied to
   every configured workspace, including the ones `SLACK_SWEEP_EXCLUDE`
   keeps out of the operator's view. An AFK meant for the workspaces the
   operator works in landed in one they do not follow.
3. A caller that passed a permalink as `url` had it silently ignored and
   got "pass a permalink" back, although one had been passed.

## Decision

1. A reply is *addressed* to the operator when it sits in a thread they
   take part in — the own-thread backstop marks the root
   (`ChannelUnread.OwnThreads`), they wrote the parent, or a reply in it is
   theirs — or when it @-mentions them. Addressed replies render at the DM
   cap (1500 chars) via `format.WithAddressedReplies`; other replies keep
   the compact preview. Bounded, not full text, so a pasted wall of text
   still cannot blow the budget.
2. You-global changes resolve targets through `selfTargets`: an empty
   workspace follows the sweep (excluded workspaces are left unchanged and
   listed with how to reach them), `all` or `*` reaches every workspace, a
   label reaches that one.
3. `permalinkArg` reads `permalink`, then `url`, then `link`, for every tool
   that takes a permalink.

## Consequences

- Answers in the operator's threads are readable in the sweep itself.
- Setting AFK without a workspace no longer touches excluded workspaces;
  the result says which ones were left alone. Callers who want every
  workspace pass `workspace=all`.
- The sweep output grows only by the addressed replies, which are few.

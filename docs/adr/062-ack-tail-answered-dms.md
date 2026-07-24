# ADR 062: ack-tail detection — answered DMs with a trailing "thanks"

Date: 2026-07-24
Status: accepted

## Context

The operator reported "the sweep doesn't see my DM replies" again after
ADR 059. Forensics with ground-truth history showed the actual pipeline:
for DMs the operator actively replies in, `last_read` is usually caught
up — the counterpart messages get RE-INJECTED by the search-based
backstops (`to:me` / `from:me`, ADR 048/050/053), and search never
returns the operator's own messages. So an active DM renders as
counterpart-only "unanswered" noise. ADR 059's suppression only covered
the operator-holds-the-last-word case; the common real ending —
operator answers, counterpart closes with "Спасибо! Прилетел" — kept
the DM visible, because the counterpart technically spoke last. The
closing-ack regex also required the WHOLE message to be an ack, so
ack-plus-one-word closers slipped through mentions filtering too.

## Decision

1. `isClosingAckText` (shared): exact regex fast-path, plus a NARROW
   heuristic — exactly two words, no question mark, first word an ack
   token ("Спасибо! Прилетел", "Ок, принял"). Longer ack-prefixed
   messages ("спасибо за информацию, посмотрю") stay live: an ack with
   a promise of action is content, per the existing mentions-filter
   contract. Used by both drop_closing_acks and the DM probe.
2. Answered-DM probe widens from latest-1 to a 5-message window
   (`answeredDMWindow`), newest-first: a DM is answered when the
   operator has spoken in the window AND every counterpart message
   after the operator's last is a closing ack. Fail-open unchanged.

## Consequences

- The "answered but counterpart said thanks last" DMs collapse into the
  one-line hidden note; genuine follow-up questions still surface.
- Probe cost unchanged in calls (one history read per DM; limit 1→5).
- 588 → 589 tests (ack-tail window semantics, ack-text tiers incl. the
  regression case from the mentions contract). Minor release (1.19.0).

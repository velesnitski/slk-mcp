# ADR 061: canvas selectors — date/match/list for meeting-notes lookup

Date: 2026-07-23
Status: accepted

## Context

Meeting notes land in canvases titled with a date ("22.07.2026 Tech
Meet"), one per meeting, accumulating in the channel. read_canvas
(ADR 058/060) could only fetch "the newest" — but "did yesterday's
meeting notes land?" is a lookup by TITLE date, not by file Created
(a notes canvas may be created early or edited later), and a channel
holds many. The operator's real question — meeting-status — needed
either-way resolution in one call.

## Decision

read_canvas gains selectors over the channel's FULL canvas set (shared
canvas files ∪ the attached tab canvas, deduped by file id):

- `date` (YYYY-MM-DD): match the title against generated spellings —
  23.07.2026, 23.07.26, 2026-07-23, 23.07, 23/07/2026, plus unpadded
  forms (`canvasDateVariants`, pure). Titles carry meeting dates;
  Created does not.
- `match`: case-insensitive title substring ("Tech Meet"); combines
  with date (AND).
- `list_only`: list titles + created dates, no download.
- Selection = newest Created among title-matches (`selectCanvas`, pure).

**A miss is an answer, not an error**: "no canvas matching X; available:
<list>" returns as normal text — one call answers "did the notes for
day X land?" in both directions. No-selector behaviour unchanged
(tab → newest shared).

## Consequences

- Meeting-status check is a single call; the assistant workflow is
  `read_canvas(channel, date=…)` → on miss, fall back to huddle
  evidence in the digest.
- Pure helpers unit-tested: date-variant generation (incl. unpadded and
  non-ISO rejection), date+match AND-selection, newest-wins, list
  rendering. 585 → 588 tests. Minor release (1.18.0).

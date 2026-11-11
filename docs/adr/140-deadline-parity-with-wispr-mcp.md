# ADR 140: Deadline parity with wispr-mcp

Date: 2026-11-11
Status: accepted

## Context

`get_my_actions` is meant to merge commitments made in meetings, read
by wispr-mcp from its meeting notes, with commitments found in Slack.
Each server resolves deadline phrases with its own parser. When the same
phrase resolves to different days, the merged list shows one commitment
twice, each with its own deadline.

A shared table of phrases found three gaps on this side: «к вечеру»,
«к концу месяца» and "by end of month" named no deadline here, but a
date in wispr-mcp.

## Decision

- Both repositories carry the same table,
  `internal/actions/testdata/deadline_vectors.tsv` here and
  `tests/data/deadline_vectors.tsv` there. Each row holds a phrase, the
  day it was said and the expected due day. A row that fails in either
  repository is a bug in that one, and a change to the table is made in
  both.
- «к вечеру» joins the same-day strong deadlines.
- «до/к концу месяца», "end of month" and "eom" resolve to the month's
  last day, as a strong deadline.

Only dates are compared. wispr-mcp also marks fuzzy spans such as "next
week" as approximate. This server has no such flag.

## Consequences

- A commitment repeated in a meeting and in Slack gets one deadline.
- The table is the place to settle a disputed reading: add the row, then
  make both parsers pass it.

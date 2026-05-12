# ADR 001 — GIT MODE prefers MR-iid over issue-id

**Status:** accepted
**Date:** 2026-05-07
**Tag at acceptance:** v0.3.24

## Context

The git-channel renderer collates webhook bodies from a `#git-*`
channel into per-MR / per-branch / per-deploy "workflow stories".
The grouping key is what makes events about the same MR coalesce
into one line of the digest instead of three.

The natural-looking choice — group by the first `XXX-NNN`-style
issue ID found in the message — turns out to be wrong in two
recurring ways:

1. **Branch ticket ≠ MR title ticket.** GitLab webhooks for a push
   to a branch named after issue A often arrive minutes before the
   merge of an MR whose *title* references issue B. Both events
   touch the same change, but keyed on issue ID they appear as two
   independent stories. The branch lifecycle ("branch new" / "branch
   rm") gets stranded from the MR it belongs to.

2. **Multiple issue IDs in one body.** A merge-event webhook often
   carries the MR title (one issue) and the auto-generated commit
   message (a different issue, copied from a branch name). Picking
   the *first* match is non-deterministic from a human's
   perspective.

The MR-iid (`!1234`) is the unambiguous identity of the merge
request itself — Slack-rendered as `!1234`, derived from the GitLab
URL, present in every webhook for that MR. It's the only identifier
that survives across the MR's lifecycle.

### Options considered

- **a.** Group by first issue ID (status quo before this ADR) —
  rejected: produces the symptoms above.
- **b.** Group by branch name — rejected: loses cohesion when a
  branch ends up serving multiple MRs (force-push to closed-then-
  reopened MR, retargeted MRs).
- **c.** Group by MR-iid; aliases branches to their MR-iid by
  pre-scanning the message batch for branch ↔ MR co-occurrences.
  Issue ID becomes the fallback when no MR is mentioned.

## Decision

Use **(c)**. `chooseWorkflowKey` priority order:

1. MR-iid found in the message text (`!1234`).
2. Branch name with a known MR alias from this batch's pre-scan.
3. Issue ID.
4. Raw branch name.
5. Deploy target.
6. Repo only.

Implementation: `internal/digest/gitchannel.go::chooseWorkflowKey`
+ `buildBranchAliases`.

## Consequences

- The digest now correctly groups push / approval / merge / branch
  delete events that belong to the same MR.
- Workflows that touch a branch but never open an MR (deploy
  branches, long-lived release branches) are still keyed sensibly
  (fallback 4 / 5).
- Author / reviewer / merger roles, surfaced by the same v0.3.24
  change, are also keyed off the MR — they wouldn't have made sense
  on the issue-id grouping where multiple MRs could share an issue.
- The pre-scan pass adds O(N) cost to digest rendering; acceptable
  since N is bounded by the digest's message budget (200 default).

## Validation

Five regression tests in `internal/digest/gitchannel_test.go`:
`TestGroupGitWorkflows_PrefersMRIidOverIssue`,
`TestGroupGitWorkflows_BranchAliasesToMR`,
`TestGroupGitWorkflows_TracksActorRoles`,
`TestGroupGitWorkflows_MRWithoutIssueIDStillGroups`,
`TestExtractWorkflowKey` (the priority table).

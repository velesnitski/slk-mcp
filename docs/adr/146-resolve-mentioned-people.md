# ADR 146: resolve people who are only mentioned

Date: 2026-11-12
Status: accepted

## Context

Readers such as `get_thread` and `get_message` build one ID→name map per
batch of messages (`resolveRefs`) and render `<@U…>` mentions from it.
The map was filled from message authors only. A person who was
mentioned but did not post in the batch – "assign to @someone" in a
thread they never joined – was never looked up and rendered as a raw
ID, even though a `users.info` lookup would have named them. The
unread-summary path already collected mentions; the shared helper did
not.

The reader then had no quick way back from that ID to a person:
`list_users filter=` matched handle, names and title, not the ID, so a
filter on the raw ID returned nothing.

## Decision

- `resolveRefs` resolves authors **and** every user mentioned in the
  bodies (`format.CollectMentionedUserIDs`). The lookups go through the
  same cached `UserService.Name`, so a name seen once costs nothing
  more, and a failed lookup still falls back to the ID.
- `list_users filter=` also matches the Slack ID – as the whole ID only.
  IDs are random letters and digits, so a substring match would make a
  short filter such as "pm" hit people whose ID merely contains it.

## Consequences

- A thread or message mentioning someone who did not post costs one
  `users.info` call per new person, once per process.
- No output changes for mentions that already resolved.

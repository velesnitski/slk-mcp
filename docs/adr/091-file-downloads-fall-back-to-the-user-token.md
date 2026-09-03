# ADR 091: file downloads fall back to the user token

Date: 2026-09-03
Status: accepted

## Context

`read_document` could classify an HR spreadsheet correctly and still not
read it: the download came back `401 Unauthorized` for a file the
operator had open in their own Slack client.

The cause is membership, not scope. `DownloadFile` authenticated with
the primary client, which is the **bot**. A bot is a member of the
channels it was invited to; the operator is a member of everything they
can see. The files that matter most — HR material, board documents,
anything in a private channel or a group DM — live precisely where a bot
was never invited.

So the failure was systematic rather than incidental: the tool worked on
public channels and 1:1 DMs and failed on exactly the conversations
whose contents are worth reading.

## Decision

**On an auth refusal, retry the download with the user token.** The
server already holds a user client for the unread and mention surfaces;
it simply was not offered to downloads.

Three constraints keep it honest:

- **Only auth refusals retry.** A 404 is 404 for both tokens, and a
  timeout retried under a second identity is just a second timeout. The
  classifier matches 401/403 and Slack's token-refusal codes, and
  nothing else.
- **No self-retry.** When one token serves both roles the fallback is
  nil, so a refused request is never repeated identically.
- **The sink is reset first.** A refused attempt can leave a partial
  body in the writer. Where the writer supports it, it is truncated and
  rewound before the retry; where it does not, the original error is
  returned with that reason stated rather than a second body being
  appended to the first.

The error text on total failure names both attempts, so "the bot cannot
see it and neither can you" is distinguishable from "the bot cannot see
it".

## Consequences

- Documents in private channels and group DMs become readable, which is
  where review material, HR forms and exec files live.
- Downloads keep working unchanged for bot-visible files; the fallback
  costs one extra request only on refusal.
- A deployment with no user token behaves exactly as before.

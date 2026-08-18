# ADR 077: the export corpus is lossless and hashes secrets

Date: 2026-08-18
Status: accepted

## Context

`export_conversations` writes a corpus that is read again much later,
after the source window has scrolled out of reach.

## Decision

Capture is lossless. No summarising, no low-signal filtering, no
classification — a judgement made at capture time cannot be revisited,
because the source is gone by then.

What that requires storing, because it is unreconstructable afterwards:
thread structure unflattened, reactions **with their actors**, the edit
flag, attachment refs, and `reply_count` alongside `replies_fetched` so a
reader can tell a short thread from a truncated one.

Secrets are hashed, not blanked. `[redacted]` twice is indistinguishable;
`[secret:sha256:…]` twice is provably the same value, which is the only
property worth keeping — and the secret still never reaches disk. Limited
to high-confidence shapes: a pattern loose enough to catch prose
passwords would rewrite ordinary sentences.

Selection defaults to channels the operator actually posted in
(`from:me`), not joined channels and not unread ones.

Permalinks are derived from the cached `auth.test` team URL, not fetched
per message — `chat.getPermalink` per row would make any real export
rate-limit-bound.

## Consequences

- JSONL, append-only, `v` on every line; dedup key is
  `(workspace, channel, ts)`, so an overlapping re-run adds nothing.
- Output defaults to `$HOME/slk-export` (0700, files 0600) rather than
  the temp dir, which is swept out from under a long-lived corpus.
- A malformed trailing line (interrupted run) is skipped, not fatal.
- `UnreadClient` gains `TeamURL` and `ParticipationChannels`.

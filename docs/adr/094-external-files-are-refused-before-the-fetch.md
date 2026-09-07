# ADR 094: external files are refused before the fetch

Date: 2026-09-07
Status: accepted

## Context

A document linked into a conversation from a third-party service —
Drive, Dropbox, Box — arrives through the API looking exactly like an
upload. Slack returns its name, mimetype and size, and the classifier
accepts it as readable, because by every visible signal it is.

The difference only appears at fetch time: `url_private` points at the
third-party document, not at Slack, and Slack's credentials mean
nothing there. The download fails with a bare `401 Unauthorized`.

That error is actively misleading. It is the same status a genuine
Slack permissions problem produces, so it sends the reader after tokens,
scopes and channel membership — and none of those is the cause. Working
through that list takes real time, and every step of it confirms that
the Slack side is fine, which makes the next wrong hypothesis more
attractive rather than less.

The signal was available the whole time. Slack marks these files with
`is_external` and names the service in `external_type`.

## Decision

Detect them and refuse before the download.

`read_document` partitions its candidates: external files never reach
the fetch. The error explains what the file is, names the service, and
hands back the link so the caller can open it with credentials that
apply. When some candidates are local and some are not, the local ones
are read and the skipped ones are listed underneath.

`list_only` marks external entries in place. A listing that shows them
identically to readable files invites a read that cannot succeed.

Either signal is sufficient — Slack does not reliably set both — and a
missing `external_type` or `url_private` degrades to a still-useful
message rather than an empty one.

## Consequences

- The failure names its own cause and points somewhere actionable.
- No token change can fix this class of file, and the message says so,
  which stops the investigation before it starts.
- Detection is metadata-only: no request is made to learn it.

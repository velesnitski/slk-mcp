# ADR 047: workspace-aware scope diagnostics for the audio/image tools

Date: 2026-07-14
Status: accepted

## Context

The audio/image tools (`download_audio`, `transcribe_audio`, `view_image`,
`analyze_audio_tone`) hit real-world scope gaps that produced unhelpful
errors:

- A token without **files:read** makes Slack serve its HTML sign-in page
  with HTTP 200. The HTML-sniff guard (ADR pre-040) caught this, but the
  message was generic — in a multi-workspace setup it never said *which*
  workspace's token to fix.
- The v1.7.0 latest-mode reads a DM's history (`conversations.history`
  → **im:history**), opens DMs (`conversations.open` → **im:write**),
  and resolves handles (`users.list` → **users:read**). If any of those
  scopes is missing, Slack returns `missing_scope` and the tool surfaced
  the raw error with no guidance.

This bit us concretely: reading a voice note from a DM in a **secondary**
workspace failed because that workspace's token lacks files:read (and
likely im:history), but the error didn't make that diagnosable — it read
like a generic failure.

## Decision

Decorate authorization failures across the file surface with a
workspace-aware, actionable hint — mirroring `statusErrorHint`
(status.go) rather than inventing a new pattern.

- **files:read (HTML sign-in):** the HTML-guard now returns a sentinel
  (`errFilesReadScope`); `finishFetch` detects it with `errors.Is` and
  renders `the [<workspace>] workspace token is missing the files:read
  scope …`, naming the workspace via the existing `wsLabel`.
- **missing_scope / bad token (API errors):** `looksLikeScopeError`
  matches only Slack authorization markers (`missing_scope`,
  `not_allowed_token_type`, `invalid_auth`, …); `audioScopeError` then
  prefixes the original error and appends the exact scopes the pipeline
  needs (files:read, im:history, users:read + im:write). Every
  resolution path in `fetchFiles` (file URL, latest-mode
  resolve/author/history, message resolve) routes its error through
  `Hub.scopeResult`.

Non-scope errors (not-found, rate-limit, the custom "needs a user token"
message) pass through verbatim — the classifier is deliberately narrow so
a genuine failure is never reframed as a scope problem.

We decorate **on failure** rather than probe scopes up front: Slack's
`auth.test` doesn't return scopes (they live only in the `X-OAuth-Scopes`
response header, which slack-go doesn't surface), so a real preflight
would cost an extra round-trip on every call for information the failure
already carries. The failure is the cheapest scope probe.

## Consequences

- A scope failure now tells you which workspace token to fix and which
  scope to add — the secondary-workspace voice-note case is
  self-diagnosing instead of opaque.
- Pure helpers (`looksLikeScopeError`, `audioScopeError`) + the sentinel
  are unit-tested: scope-vs-not classification, decoration content,
  verbatim pass-through, single-workspace label cleanliness, and
  `errors.Is` matchability. 542 → 547 tests.
- No behaviour change on the happy path and no new API calls; purely
  better diagnostics. Patch release (1.7.1).
- Not fixed here (config, not code): a secondary workspace's Slack app
  still needs files:read + im:history (+ users:read/im:write for @handle
  DMs) added and reinstalled for latest-mode to work against its DMs.
  This ADR only makes that gap legible.

# ADR 022 — normalize sender handles in the bot-mention filter

**Status:** accepted
**Date:** 2026-06-05
**Tag at acceptance:** v0.4.20

## Context

ADR 021 (v0.4.19) added `filterBotSenders` to drop automation
identities (Google Calendar, Slackbot, Drive) from `get_mentions`,
because their "@you Today is …" pings are never actionable. The
match was an exact, lowercased lookup against a set of underscore
handles (`google_calendar`, `googledrive`, …).

It didn't work in production. The morning after shipping v0.4.19,
the calendar bot's daily ping was *still* surfacing as a pending
mention. Cause: the same bot reports a **different handle spelling
per API surface**. `search.messages` (which backs `get_mentions`)
returns the calendar bot's `Username` as `"google calendar"` —
**with a space** — while the conversations listing shows
`"google_calendar"` with an underscore. The v0.4.19 set only had the
underscore form, so the space form sailed through.

This is a textbook dogfooding catch: the unit test used the
underscore form (matching the set), so it passed, while the live API
returned the space form. The test encoded the same wrong assumption
the code made.

## Decision

Match on a **normalized** handle on both sides:

```go
func normalizeSender(s string) string {
    return strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(s))
}
```

The `automationSenders` set now stores normalized keys
(`googlecalendar`, `googledrive`, `slackbot`, `uslackbot`), and the
lookup normalizes `m.Username` the same way. So `"google calendar"`,
`"google_calendar"`, `"Google Calendar"`, `"google-drive"` all fold
to the same key. The `USLACKBOT` user-id sentinel check is unchanged.

The test now asserts the **space** form explicitly (the exact case
that slipped through), plus title-case and hyphen variants, and a
dedicated `TestNormalizeSender` table.

## Consequences

- **The calendar/Slackbot/Drive false positive is actually gone**,
  regardless of which separator the API happens to return.
- **Lesson recorded:** when filtering on an external identifier whose
  formatting isn't under our control, normalize separators rather
  than enumerate spellings — and make the test use the form the
  *live API* returns, not the form that matches the code.

## Validation

- `go test -race -count=1 ./...` — green.
- `TestFilterBotSenders` now includes the space form; new
  `TestNormalizeSender` table pins the folding rules.
- Live: after rebuild + reconnect, the Google Calendar daily ping no
  longer appears in `get_mentions(pending_only=true)`.

## Out of scope

- Matching bots by `bot_id` / app-id instead of handle. More robust
  in principle, but the search API doesn't reliably expose it on
  these hits, and the normalized-handle approach covers the observed
  cases. Revisit if a bot appears with a handle that normalization
  can't catch.

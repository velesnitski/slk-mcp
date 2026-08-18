# ADR 075: canvas edits reach the unread sweep

Date: 2026-08-18
Status: accepted

## Context

The operator was @-mentioned inside a channel canvas. Slack sent the
notification; `get_unread_summary` reported nothing at all.

Nothing was broken. Every backstop the sweep has — unread history, the
DM window (ADR 009), the `to:me` thread-mention pass (ADR 010), the
`from:me` own-thread pass (ADR 051), the lookback behind `last_read`
(ADR 073) — reads *messages*. A canvas is a file-backed document. Editing
one, including adding a line that tags someone, produces **no channel
message**: `conversations.history` has nothing to return, and
`search.messages` does not index canvas bodies. `read_canvas` could see
it, but only when the caller already names the channel — which requires
knowing the answer first.

So the gap was structural, not a missed case: the sweep's entire input
model excluded a class of activity Slack still notifies on.

## Decision

Add a canvas delta computed independently of the message pipeline, and
render it as its own section of the sweep.

**Discovery bypasses slack-go.** `goslack.File` exposes `Created` and
`Timestamp` and drops files.list's `updated` field entirely. Through the
typed SDK an *edited* canvas is indistinguishable from an untouched one,
so only brand-new canvases would be detectable — the opposite of the
reported case, which was an edit to a canvas that had existed for weeks.
`CanvasService.RecentCanvases` therefore speaks raw HTTP to `files.list`
and decodes `updated` by hand, reusing the `ListService` precedent
(ADR 018) rather than inventing a second style.

Two consequences of the API shape are worth recording:

- `ts_from`/`ts_to` filter on **create** time, so they cannot narrow this
  query at all. The page is fetched unfiltered and selected on `updated`
  client-side.
- The call needs the user token. A bot token only sees canvases in
  channels it was invited to — precisely the blind spot being closed —
  so a missing user token is a sentinel error, not a silent empty result.

**Mentions are found by reading the document.** There is no API that
answers "does this canvas tag me", so the newest few changed canvases
(`canvasProbeLimit = 6`) are downloaded and scanned for `<@Uxxx>`. The
listing cap and the download cap are deliberately separate: listing is
one request for everything, probing is one request each. Canvases past
the probe cap are still reported, labelled `body not checked` — an
unchecked canvas must not read as "no mention found".

**The section survives an empty sweep.** A canvas edit can be the only
thing that happened since the cursor, so the block is emitted even when
`results` is empty, instead of the "all caught up" line. When `after=` is
passed, the cursor wins over `canvas_hours` so canvases follow the same
delta as messages and a re-pull cannot re-report the same edit.

Mention-carrying canvases sort above newer untagged ones. Whole feature
is best-effort: any failure logs and omits the section, never fails the
message sweep.

## Consequences

- New `canvas_hours` parameter on `get_unread_summary` (default 24, 0 =
  off). Cost when nothing changed is one `files.list` call per workspace.
- `CanvasService` now carries the user token and an HTTP client, so
  `newCanvasService` takes a token argument.
- `CanvasClient` gains `RecentCanvases`; the compile-time assertion in
  `internal/tools/contracts.go` keeps the seam honest.
- Rendering, selection, parsing, labelling and the relative-time stamp
  are pure functions, unit-tested without any API.
- Not covered: a canvas edited by the operator themselves still appears
  (harmless, and cheaper than a second lookup), and a mention added then
  removed between two pulls is missed — the body is read at pull time,
  not diffed.

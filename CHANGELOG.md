# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.41.0] - 2026-09-02

### Added

- **`read_document` reads `.xlsx` workbooks**, flattened per sheet and
  **with their cell comments** — a spreadsheet sent for review carries
  the review in its comments, so extracting only the grid would drop the
  part worth reading. Parsed with `archive/zip` + `encoding/xml` rather
  than a spreadsheet library: this server is public and parses files
  other people hand it, so the dependency it does not have is attack
  surface it does not have. Dates render as serial numbers rather than
  guessed formats. Capped at 20k cells / 64 columns, reported when hit.
  See ADR 089.

## [1.40.1] - 2026-09-02

### Added

- **Releases publish themselves.** `.github/workflows/release.yml` fires
  on a `v*` tag and creates the GitHub Release from that version's
  `CHANGELOG.md` section, read through the new
  `scripts/changelog-section.sh` so the notes on GitHub and in the repo
  share one source. A tag with no changelog entry fails the workflow
  rather than publishing empty notes; re-running on an existing release
  updates it. Releases had drifted seven tags behind because publishing
  was manual.
- **`.github/dependabot.yml`** watches Go modules and the pinned GitHub
  Actions weekly, targeting `dev` (everything reaches `main` by
  fast-forward). Minor and patch updates are grouped into one PR;
  majors stay separate.

See ADR 088.

## [1.40.0] - 2026-09-01

### Fixed

- **Thread replies whose parent is out of window are no longer dropped.**
  A sweep would report "N thread replies" in its header and render none
  of them: `ChannelDigest` is driven by top-level messages, so a channel
  whose only new content was replies to an older parent collapsed to ""
  under `WithOmitEmpty` and was skipped — after the counters had already
  counted it. This hides escalations specifically, since an escalation is
  usually a reply to a report filed hours earlier. Such replies now
  render as their own block, ordered by parent timestamp and labelled
  with the parent's time. `WithOmitEmpty` still drops a genuinely empty
  channel. See ADR 087.

## [1.39.0] - 2026-09-01

### Fixed

- **`read_canvas` resolves mentions.** It was the only read surface that
  did not, so a canvas came back as raw `@U…` ids — and directly
  contradicted the unread summary, which flags a canvas as *mentioning
  you* and then left no way to find where. Canvases download as HTML and
  the flattener strips tags, taking `<@U…>`'s angle brackets with them,
  so the new `format.RenderCanvasText` resolves the bare form too.
  Message rendering is unchanged: `RenderText` still ignores bare
  mentions, and a test holds that line. Unresolvable ids are left as-is
  rather than guessed. See ADR 086.

## [1.38.0] - 2026-09-01

### Fixed

- **The `pre-push` hook now scans the commits being pushed, not all of
  history.** Git hands a pre-push hook the exact range on stdin;
  `sweep.sh` gained `--range` to use it. Scanning all history meant the
  first deny-list pattern matching an already-published string blocked
  *every* subsequent push, for content the developer could not affect —
  leaving only `--no-verify` or a weaker deny-list as exits. `make
  sweep-history` still audits the whole object store, which is the right
  scope before publishing a repo or after a rewrite. Amends ADR 083;
  see ADR 085.

### Changed

- **A channel that cannot be resolved now names the closest matches.**
  `channel #orbit-relay not found` becomes `channel #orbit-relay not
  found — did you mean #orbit-relay-alerts, #orbit-relay-monitoring?`
  The lookup already walked and cached every channel in the workspace
  before failing, so the suggestions cost nothing extra. Ranking follows
  how people actually miss: shorthand prefixes first, then containment,
  then a bounded edit distance for typos. Unrelated input still gets a
  bare "not found". See ADR 084.

## [1.37.1] - 2026-08-31

### Added

- **`make hooks`** installs a tracked `pre-push` hook that runs the full
  sweep (`--history`) before every push. The deny-list is untracked by
  design, so CI can only run the shapes-only scan; putting the list in
  an Actions secret was rejected because this repo is public and the
  list's contents are themselves the sensitive part. The full scan now
  runs where the list already lives. `git push --no-verify` remains the
  visible override; no env-var bypass was added.

### Fixed

- **`scripts/sweep.sh --history` now scans reachable objects only.**
  `--batch-all-objects` also read unreachable ones, so staging a secret
  and then correctly unstaging it left a dangling blob that blocked
  every push until gc ran. A push publishes what is reachable from refs;
  that is now what gets scanned. Found by the new hook's own negative
  test before it shipped.

See ADR 083.

## [1.37.0] - 2026-08-31

### Fixed

- **`read_document` reads `.log`, `.conf` and other octet-stream
  attachments.** Classification gained a third tier — the filename —
  consulted only when the mimetype is generic. Slack serves anything
  outside its own extension table as `application/octet-stream` with
  `filetype: binary`, which is the normal shape of operational evidence,
  and both earlier tiers rejected it. An explicit `image/*`, `audio/*`,
  `video/*` or zip mimetype still wins over a misleading extension.
- **`list_only` no longer omits attachments it cannot read.** It
  filtered before rendering, so an inventory of a conversation looked
  complete while silently dropping every non-text file. All attachments
  are now listed, with the non-text ones marked and pointed at
  `view_image` / `transcribe_audio`. Reads still filter.

### Security

- Rendered documents pass through `export.Redact` before truncation, so
  key material in a newly-readable `.ovpn` or `.conf` never reaches the
  transcript. The number of redacted spans is reported, never silent.
- **Credential-shaped test fixtures are assembled at runtime** instead of
  written as literals. They were never usable secrets, but a scanner
  matches on shape, not validity, and this repository is public.
- **`scripts/sweep.sh` no longer honours `sweep:allow` in the working
  tree.** With the fixtures assembled, the exemption had no users, so a
  credential shape in a tracked file now always fails — the guard has no
  escape hatch left to reach for. The marker survives for `--history`
  only, where the old blobs are immutable; deny-list patterns still
  apply to marked lines even there.

See ADR 081 and ADR 082.

## [1.36.0] - 2026-08-25

### Added

- **`get_message`** — fetch ONE message verbatim: full text with no
  truncation, plus the metadata every preview drops (absolute time,
  edited flag, reactions with counts, files, thread position, rune
  count). The drill-in for any "(+N chars)" preview. Pass a permalink —
  the workspace is auto-detected from the link's host via the cached
  auth.test URL — or channel + ts. A thread reply resolves through its
  own thread_ts and shows the parent as one capped context line.
  Requested by the tool's second user. See ADR 080.

## [1.35.0] - 2026-08-19

### Changed

- **`scripts/sweep.sh` now fails closed.** A missing or empty
  `.sweep-patterns.local` exits 2 instead of scanning a subset and
  printing "clean" — the reduced scan is now the explicitly named
  `--shapes-only`. Adopts the established sweep contract:
  tracked `.sweep-patterns.example` template, `CS:` prefix for
  case-sensitive patterns, tracked-file count printed on success, and
  plain `grep` over `git ls-files` rather than `git grep`.

  The `sweep:allow` marker is now **partial** — it exempts a line from
  the credential-shape rules only, never from the deny-list.

### Added

- `--quiet` reports `file:line` without the matched text, for public CI
  logs that a history rewrite can never reach.
- A CI sweep job. With the `SWEEP_PATTERNS` secret it scans full history;
  without it, credential shapes only, and it announces which.

See ADR 079, which supersedes the CI consequence of ADR 076.

## [1.34.0] - 2026-08-18

### Fixed

- **Bot feeds no longer evict human channels under `max_chars`.**
  `RankUnread` based the rank on raw message volume plus an urgency
  score that rewards `error`/`failed`/`alert` — the two things a
  machine-driven channel is made of. A fifty-line alert feed outranked
  every low-volume human channel and pushed them into the omitted-by-cap
  footer.

  Adds `feedPenalty`, a rank tier below ordinary channels, symmetric to
  the existing DM tier above them. `IsBotFeed` reuses the log/git/
  low-signal detectors the renderer already applies, so the ranker and
  the renderer cannot disagree about what a feed is. `mentionBonus`
  still stacks: a ping inside a CI channel surfaces, it just ranks below
  a ping from a person. See ADR 078.

### Changed

- The `max_chars` footer lists each omitted channel with its unread
  count, so a drill-in can be chosen rather than guessed.

## [1.33.0] - 2026-08-18

### Added

- **`export_conversations`** — appends conversations to a local
  append-only JSONL corpus and returns the path. Capture is lossless:
  thread structure, reactions with their actors, edit flags, attachment
  refs, derived permalinks, and `reply_count` vs `replies_fetched`.
  Credential-shaped spans are replaced with a stable hash placeholder by
  default. Selection defaults to channels the operator posted in.
  Re-running over an overlapping window adds nothing. See ADR 077.

- `UnreadService.TeamURL` and `UnreadService.ParticipationChannels`.

## [1.32.0] - 2026-08-18

### Added

- **Canvas activity in `get_unread_summary`** — new `canvas_hours`
  parameter (default 24, 0 = off) reports canvases updated since the
  cursor and flags the ones that @-mention the operator.

  A canvas edit produces no channel message: Slack notifies whoever is
  tagged inside the document, but `conversations.history` has nothing to
  return and `search.messages` does not index canvas bodies. Every
  existing backstop reads messages, so a tag added inside a canvas was
  structurally invisible to the sweep.

- `CanvasService.RecentCanvases` — workspace-wide canvas delta over a raw
  `files.list` call. slack-go's `File` drops the `updated` field, which
  makes an *edited* canvas indistinguishable from an untouched one
  through the typed SDK; this decodes it directly. Requires the user
  token (a bot token only sees canvases in channels it joined).

### Notes

- The canvas section is emitted even when there are no unread messages —
  a canvas edit can be the only thing that happened.
- When `after=` is passed it wins over `canvas_hours`, so canvases follow
  the same delta as messages.
- Only the newest 6 changed canvases are downloaded to check for a
  mention; the rest are listed as `body not checked`.
- See ADR 075.

## [1.31.0] - 2026-08-12

### Added — `read_document` accepts PDFs
Two decks arrived in a channel as PDF and the tool answered "no matching
attachment", because it only accepted text types. A PDF is a document in
every sense that matters, and it is the format proposals and decks
actually circulate in. PDFs are now downloaded and returned as a local
path rather than flattened: parsing PDF in Go would mean a dependency and
a lossy extraction that drops layout, tables and figures, when the caller
already has a reader that renders them properly. Text documents are
unchanged, still rendered inline and deleted straight after. The strict
sign-in-page guard deliberately keeps applying to PDFs. See ADR 074.
619 → 621 tests.

## [1.30.0] - 2026-08-10

### Fixed — a thread you are actively watching no longer vanishes from the sweep
A delta pull reported "all caught up" while the incident thread being
followed had grown from 3 messages to 15. Cause: `Unread` pulls history
from `last_read`, and `fetchReplies` only walks thread parents found in
that result, so a thread whose parent is already read is structurally
invisible however active it is. Reading a channel permanently removed its
existing threads from the sweep, and the three earlier backstops (057,
062, 064) each worked around that rather than removing it.

History is now pulled from `min(last_read, now - 12h)` and the full page
is used as the parent list, while the unread message list keeps its exact
previous meaning. Pure `activeThreadParents` keeps only parents whose
`latest_reply` is newer than `last_read`, using a field Slack already
returns, so the wider window costs one larger page rather than extra
calls. See ADR 073. 618 → 619 tests.

## [1.29.0] - 2026-08-10

### Fixed — `read_document` can reach the document that is not the newest
First real use of 1.28.0 hit the next wall: two documents posted three
minutes apart, and latest-mode could only return the later one — the
earlier was unreachable without hunting down its message timestamp by
hand. `read_document` now takes `match` (case-insensitive filename
substring), `limit` (default 1), and `list_only`, matching the selector
shape `read_canvas` already uses. Selector mode engages only when one of
them is set and no permalink/timestamp was given; naming an exact message
still wins. A `match` that hits nothing errors with the needle quoted and
points at `list_only`, rather than silently falling back to the newest
file. Underneath, `RecentFileMessages` collects candidates from the top
level and recent threads, de-duplicates by timestamp and sorts
newest-first; `LatestFileMessage` keeps its one-call short-circuit.
See ADR 072. 614 → 618 tests.

## [1.28.0] - 2026-08-10

### Added — `read_document`: text attachments are finally readable
Pictures had `view_image`, sound had `transcribe_audio`, and documents —
the form decisions actually circulate in — had nothing, so a colleague's
"tell me what you think" about two exported HTML files could not be
answered from inside the session. `read_document` resolves an attachment
the same way the audio tools do (permalink, file URL, channel +
timestamp, or newest-in-conversation with an optional `from` filter) and
returns it inline: HTML flattened to plain text, everything else as-is.
Accepts `text/*`, textual `application/*`, and Slack's own `filetype` for
snippets that ship without a mimetype. Temp files are deleted before
returning. Truncation at `max_chars` (default 40000) is always reported,
never silent.

### Fixed — an HTML attachment is no longer reported as a missing scope
`downloadFiles` treated any HTML body as proof that the token lacks
`files:read`, since Slack serves its sign-in page with HTTP 200. Correct
for audio, video and images; wrong for an `.html` file, which would be
rejected for being what it is. For attachments expected to contain text
the check is now "looks like Slack's sign-in page" (HTML doctype plus a
login marker) instead of "looks like HTML"; binary attachments keep the
stricter rule. See ADR 071. 607 → 614 tests.

## [1.27.0] - 2026-07-31

### Fixed — "newest voice note" now looks inside threads
Latest-mode (`transcribe_audio`, `analyze_audio_tone`, `download_audio`
with a channel and no timestamp) answered "no recent message with a
matching attachment" for a note sitting in that very DM, because
`conversations.history` returns top-level messages only and the note was
a thread reply. Only the permalink path worked. When the top level has no
match, the recent threads are now walked (up to 12, newest-first) and the
newest qualifying reply wins, compared by timestamp rather than by first
hit; the `from` filter applies inside threads too. The common case still
costs one history call. See ADR 070. 604 → 607 tests.

## [1.26.0] - 2026-07-31

### Fixed — the actual cause of truncated transcripts: the `-nt` flag
1.24.0 and 1.25.0 each fixed a real failure mode, but neither was the
one biting: the clip carried a single audio stream at -22 dB mean, and
the silence guard never fired. Running the pipeline by hand showed
whisper finishing 87 seconds of audio in 1.0 second — dropping flags one
at a time pinned it on `-nt` / `--no-timestamps`, which in whisper.cpp
1.9.x collapses the decoder into a few repeated tokens and stops after
the first segment. The result is not an error but a short, confident,
well-formed string that reads like a transcript. `-nt` is gone; whisper's
timestamped segments are now converted to flowing text in Go, and a
regression test forbids the flag from returning. See ADR 069.
601 → 604 tests.

## [1.25.0] - 2026-07-31

### Fixed — transcription now mixes every audio stream
Follow-up to 1.24.0: the clip that "had no sound" did contain speech.
Screen recordings and huddles often carry two audio streams (system +
microphone), and the conversion let ffmpeg pick one, so a mic on the
non-default stream extracted as silence. The converter now counts audio
streams (ffprobe) and mixes them all
(`amix=inputs=N:duration=longest:normalize=0`); single-stream files keep
the previous command exactly. Also: `download_audio` now accepts video
attachments, so a huddle/clip permalink no longer resolves in
`transcribe_audio` but fails in `download_audio`. See ADR 068.
598 → 601 tests.

## [1.24.0] - 2026-07-31

### Fixed — silent recordings no longer return hallucinated transcripts
`transcribe_audio` answered a 1:27 clip with three repeated tokens and
presented them as speech; the recording had no microphone track, and
whisper hallucinates on silence. Every pipeline step "succeeded", so
nothing flagged the output as fabricated. The converted WAV is now
measured with ffmpeg `volumedetect` first: below -50 dBFS mean the call
fails with the measured level and the likely cause, and whisper never
runs. Fail-open by design — an unreadable measurement still transcribes.
See ADR 067. 595 → 598 tests.

## [1.23.0] - 2026-07-30

### Fixed — list_scheduled_messages no longer implies an empty queue
The tool reported "no scheduled messages" while the operator had 5
messages scheduled from the Slack client — a false negative.
`chat.scheduledMessages.list` only returns messages scheduled **via the
API**; UI-scheduled ones live in Slack's drafts store and are
unreachable with an xoxp token (same dead end as
`subscriptions.thread.*`). The empty case now says "no API-scheduled
messages" with the caveat and points to Slack → Drafts & sent, and the
tool description forbids reporting the queue as empty from this tool
alone. See ADR 066. 594 → 595 tests.

## [1.22.0] - 2026-07-29

### Changed — with_replies defaults per conversation kind
Reading a DM with `get_channel_digest` missed the counterpart's
substantive reply when it lived in a thread: `with_replies` defaulted to
false everywhere. It now defaults **ON for DMs** (a thread reply there
IS the conversation) and stays **OFF for channels** (fan-out cost); an
explicit argument always wins. `isDMRef` decides from the reference
shape alone — no API call. See ADR 065. 593 → 594 tests.

## [1.21.0] - 2026-07-29

### Fixed — answered-DM suppression treats threads as separate lanes
A counterpart's live question posted as a **thread reply** was invisible
to the answered-DM probe (`conversations.history` returns top-level
only), so a DM got hidden just because the operator's newest *top-level*
message — on an unrelated topic — was later in wall-clock time.
`isAnsweredDM` now checks every thread first via pure
`threadEndsWithLiveCounterpart`: any lane whose newest substantive
message is from the counterpart keeps the DM visible. Uses the
already-fetched replies — zero extra API calls. See ADR 064.
591 → 593 tests.

## [1.20.0] - 2026-07-27

### Fixed — DM bodies no longer truncated to "(+N chars)" in the sweep
get_unread_summary truncated every body to the 280-char preview — fine
for channels, wrong for DMs, where amounts/deadlines/asks live (a
billing DM showed "(+866 chars)", forcing a full_text re-fetch). DM
channels now render at a generous 1500-char cap (`dm_full_text`, default
true); channels stay compact. New `format.WithMessageLimit(n)` powers
it. Bounded on purpose — unbounded would risk a wall-of-text DM being
dropped by the budget cap entirely. See ADR 063. 589 → 591 tests.

## [1.19.0] - 2026-07-24

### Fixed — answered-DM detection sees through ack tails
Root cause of "sweep doesn't see my replies": for actively-answered DMs
the search-based backstops re-inject counterpart messages (search never
returns your own), and ADR 059's suppression missed the most common
ending — you answer, they close with "Спасибо! Прилетел". The probe now
reads a 5-message window and treats a DM as answered when your reply is
followed only by closing acks; shared `isClosingAckText` gains a narrow
two-word ack+tail tier (also used by drop_closing_acks), while
ack-plus-content ("спасибо за информацию, посмотрю") stays live. See
ADR 062. 588 → 589 tests.

## [1.18.0] - 2026-07-23

### Added — canvas selectors: date / match / list_only
Meeting notes live in per-meeting canvases titled with a date
("22.07.2026 Tech Meet"). `read_canvas` now takes `date` (YYYY-MM-DD,
matched against common title spellings incl. unpadded), `match` (title
substring) and `list_only`, applied over the channel's full canvas set
(shared files ∪ tab canvas). A miss returns "no canvas matching …;
available: <list>" as a normal answer — one call resolves "did the
meeting notes for day X land?" either way. See ADR 061. 585 → 588 tests.

## [1.17.0] - 2026-07-22

### Fixed/Added — canvas lookup cascade in read_canvas
"No canvas attached" on a channel that visibly held one: canvas
visibility on conversations.info differs between bot and user tokens,
and a "canvas in the channel" is often a standalone document shared as a
file — invisible to properties.canvas entirely. New CanvasService tries
both identities for the channel canvas tab, then falls back to
`files.list types=canvas` (user identity first), rendering the newest
shared canvas through the same download path. See ADR 060. 584 → 585
tests.

## [1.16.0] - 2026-07-22

### Added — answered-DM suppression in get_unread_summary
Slack advances `last_read` on client focus, not on send — a DM answered
from a notification stays "unread" server-side and re-surfaced the
counterpart's questions as pending after the operator had already
replied. The sweep now probes each DM's actual newest message
(`history limit=1`, immune to last_read lag) and **suppresses DMs where
the operator holds the last word** to a one-line note
(`N answered DM(s) hidden: @a — pass show_answered=true to include`).
Fail-open on probe errors; skipped when `dm_window_hours` explicitly
requests already-read DM recaps. See ADR 059. 581 → 584 tests.

## [1.15.0] - 2026-07-22

### Added — read_canvas: read a channel's canvas document
A Slack canvas is a file-backed document hung off a channel, not a
message, so every history-based tool was blind to it — teams' runbooks,
on-call rotations and process drafts live there. New **`read_canvas`**
resolves the channel (reusing ADR 054's `@handle`/`U…`/`#name`
resolution), reads `conversations.info` → `properties.canvas.file_id`,
then fetches the body like any Slack file (`files.info` → download
`url_private` via the audio pipeline's `DownloadFile`). Pure
`canvasToText` renders HTML canvases to text and passes markdown/plain
through, with a 200 KB cap. Read-only; no new client contracts. Needs
`files:read` (already required by audio/image). See ADR 058. 577 → 581
tests.

## [1.14.0] - 2026-07-21

### Added — with_replies: thread drill-in for get_channel_digest
`conversations.history` returns only top-level messages, so a channel
whose real content lives in threads (a huddle's discussion, a request
answered in replies) rendered as bare `(N replies)` counters — the
digest *said* there were replies but couldn't show them, and Slack
search lags on fresh thread replies so `get_user_messages` missed them
too. New **`with_replies`** (default false) fetches every thread in the
window and inlines the replies as `↳` lines (reuses the unread sweep's
render path); `thread_preview_replies` caps per-thread depth (default
10). Best-effort per thread; one `conversations.replies` call per thread
only when enabled. Multi-channel digest / morning recap stay reply-free
by design (fan-out cost) — drill into one channel to expand. See ADR
057. 574 → 577 tests.

## [1.13.0] - 2026-07-20

### Added — @handle / user-id conversation refs in digest & thread tools
Reading a DM by person required search→extract-D-id→digest (observed ~6×
in one day). `get_channel_digest` and `get_thread` now accept a DM as
**`@handle`** or a **bare `U…`/`W…` user id** — including the `#U…`
shape the unread summary's own DM headers print — via a shared, pure
`classifyConversationRef` + the same `@handle→OpenDM` path the audio
tools use (ADR 046). `resolveConversation`/`resolveAuthor` moved to
`conversation.go`; new `slack.IsUserID`. Additive: channel names and
canonical ids behave exactly as before. See ADR 054.

### Added — combined per-workspace delta cursor
Multi-workspace pulls emitted one cursor per workspace but `after` took
a single ts — the delta loop had to min() them by hand, re-showing
messages in the faster workspace. Output now ends with one trailing
`cursor: primary=<ts>;secondary=<ts>` token; `after` accepts that combined
shape (exact per-workspace filtering) **and** the old plain ts. Quiet /
errored workspaces carry their cursor forward (never regresses). See
ADR 055.

### Added — get_mentions summary mode
`summary: true` returns operational-load aggregates instead of the hit
list: total, DM/channel split, per-sender and per-channel counts
(top-10 + "+N more"), composing with `pending_only` and riding the
dm_history backstop for saner counts than raw Slack search. Built for
"how often was I pinged, by whom" reporting. See ADR 056.

567 → 574 tests across the three features.

## [1.12.0] - 2026-07-20

### Fixed — get_mentions no longer misses just-arrived DMs (search lag)
`get_mentions` found messages via Slack's `to:me` search, whose index
**lags on DMs** — a message the other party sent minutes ago was often
missing, so a fresh DM reply was silently dropped (history-based tools
saw it immediately). New **DM history backstop** (`dm_history`, default
true): after the search, `buildMentions` folds in recent DM history
(`RecentDMActivity`, real-time) as synthetic hits — from others only,
deduped by channel+ts (real hit wins), re-sorted newest-first. Synthetic
permalinks carry `?thread_ts` so `with_context`/reply-parsing behave
identically. `pending_only`/`strict_mention`/`with_context` unchanged.
`dm_history=false` restores search-only speed. Not an slk-mcp bug (it's
the search backend) but closes the reliability gap. See ADR 053.
564 → 567 tests.

## [1.11.0] - 2026-07-17

### Added — with_context for search_messages
A search hit is one isolated message — a `from:@user` search shows only
that user's lines, never the reply it answered, which makes a hit easy to
misread out of context. `search_messages` gains **`with_context`** (bool)
+ **`context_messages`** (int, default 3), reusing the `get_mentions`
machinery to inline a few messages before (`↳`) and after (`↪`) each hit.
Off by default (output unchanged); one/two `history` calls per hit only
when enabled. Not a bug fix — `search_messages` worked correctly — but it
closes the isolated-hit misread failure mode. See ADR 052.

## [1.10.0] - 2026-07-16

### Added — surface new replies in threads you started or replied in
`get_unread_summary` missed a class of unread: a colleague answering a
thread **you started** (or already replied in) **without @-mentioning
you**. Slack marks a channel unread only for new top-level messages;
`UnreadAll` only fetches replies for unread parents (yours is read); and
the `to:me` mention pass only catches @-mentions — so the reply fell
through every pass. New **own-thread backstop** (`own_thread_hours`,
default 24): `search from:me` finds the threads you're active in, then
surfaces replies from others **newer than your last message** in each
(pure `unseenAfterMine`). Merged via the existing thread-mention merge
(augment, never replace). Needs `Self()`; degrades to no-op without a
search backend. See ADR 051. 560 → 564 tests.

## [1.9.0] - 2026-07-15

### Added — list_scheduled_messages: see what you have queued to send
The digest shows what came in; nothing showed what you have queued to go
**out**. New read-only **`list_scheduled_messages`** over
`chat.scheduledMessages.list`: your pending scheduled messages with send
time, target channel, and a preview — you-global (empty `workspace` lists
across every configured workspace), soonest-first. Built on a per-identity
`ScheduledService` (user token; bot-only workspaces skipped). Not gated on
`SLACK_READ_ONLY` (reads nothing). Rune-safe previews (Cyrillic never
split). See ADR 050. Tool count 23 → 24. 554 → 560 tests.

### Note — thread-unfollow spike rejected
A prototype to silence reply notifications for a single thread was tried
and dropped: Slack's `subscriptions.thread.remove` returns
`not_allowed_token_type` for an OAuth (`xoxp`) token — it needs the
browser `xoxc` session token — so thread-level unfollow isn't viable
through the MCP. No `mute_thread` tool ships.

## [1.8.0] - 2026-07-15

### Added — set_dnd: pause/resume notifications (Do Not Disturb)
`set_status`/`set_presence` can *show* "do not disturb" but don't
actually silence notifications — that's Slack's DND snooze, a separate
surface. New **`set_dnd`** tool: `minutes>0` pauses notifications for
that long (`dnd.setSnooze`), `minutes=0` or `resume=true` ends the snooze
(`dnd.endSnooze`). Like status/presence it's you-global — an empty
`workspace` pauses notifications on **every** configured workspace in one
call. Built on the same user-token-only `DNDService` pattern (gated on a
user token; bot-only workspaces are skipped with a clear line). Needs the
**dnd:write** user scope; a token without it gets the scope-fix hint
instead of a bare `missing_scope`. See ADR 049. Tool count 22 → 23.
550 → 554 tests.

## [1.7.2] - 2026-07-15

### Added — reach a voice note posted as a thread reply
A voice note posted as a **thread reply** was unreachable from the thread
*root* permalink (the link you actually copy): the root message has no
attachment, and latest-mode can't see it either because
`conversations.history` excludes thread replies. Now when the message a
permalink/timestamp resolves to has no matching attachment,
`fetchFiles` falls back to scanning its **thread** (`conversations.replies`,
via the new `LatestFileInThread`) for the newest matching one — so
`transcribe_audio`/`download_audio`/`view_image`/`analyze_audio_tone`
read the reply straight from the thread-root permalink, no reply `ts`
needed. `from` stays ignored in permalink/timestamp mode. Purely additive
(a message that carries the attachment directly is unchanged; the scan
only runs where a hard error used to be). See ADR 048. 547 → 550 tests.

## [1.7.1] - 2026-07-14

### Fixed — workspace-aware scope diagnostics on the audio/image tools
A token missing **files:read** makes Slack serve its HTML sign-in page
(HTTP 200); a token missing **im:history / im:write / users:read** makes
latest-mode fail with `missing_scope`. Both surfaced as generic errors
that never said *which* workspace's token to fix — painful in a
multi-workspace setup (a voice note in a secondary-workspace DM failing
while the primary works). The file surface now decorates authorization failures with a
workspace-aware, actionable hint (mirrors `statusErrorHint`): the
HTML-guard returns a sentinel that `finishFetch` rewrites into a
`files:read` message naming the workspace, and Slack `missing_scope` /
bad-token errors get the exact scope list the pipeline needs. A narrow
classifier (`looksLikeScopeError`) ensures genuine not-found / transient
errors pass through verbatim — never reframed as a scope problem. No new
API calls (the failure is the cheapest scope probe) and no happy-path
change. See ADR 047. 542 → 547 tests.

## [1.7.0] - 2026-07-13

### Added — read the newest attachment straight from a conversation
`download_audio` / `transcribe_audio` / `view_image` /
`analyze_audio_tone` could resolve an attachment from a message
(permalink or channel+ts) or, since 1.6.0, a file URL — but the most
common ask is "read my last voice note in this DM," where you have
neither a ts nor a file link, only the conversation. `fetchFiles` gains a
**latest-mode**: pass a `channel` with no `permalink` and no `timestamp`
and it downloads the newest matching attachment in that conversation. A
`@handle` channel opens the DM (`OpenDM` via `conversations.open`); a new
`from` arg restricts by author — a `@handle`, or `"me"` for your own last
voice note (needs a user token). Newest-first selection over a 60-message
window (`LatestFileMessage`). One change, all four tools. The three
resolution paths (file URL, latest-mode, message) converge on a single
`finishFetch` tail. Selection and handle-matching extracted into pure,
unit-tested helpers (`selectLatestFileMessage`, `matchHandle`). Purely
additive — a `channel` + `timestamp` still hits the exact-message path.
See ADR 046. 536 → 542 tests.

## [1.6.0] - 2026-07-13

### Added — audio/image tools accept Slack file URLs
`download_audio` / `transcribe_audio` / `view_image` /
`analyze_audio_tone` resolved attachments only through a message
(permalink or channel+ts). But `search.messages` doesn't index an
empty-text voice memo, so the message ts can be unobtainable — the only
handle is the file's "Copy link" (`…/files/<user>/<F…>/name`).
`fetchFiles` now detects a file URL (`slack.ParseSlackFileURL`) and
resolves the attachment directly via `files.info`
(`MessageService.FileInfo`), skipping message lookup. One change, all
four tools — their `permalink` arg documents both shapes. Reuses the
confined-temp download (ADR 040); wrong-type links are reported, not
mishandled. ADR 045. 535 → 536 tests.

## [1.5.1] - 2026-07-10

### Changed — `analyze_audio_tone` pitch is now native Go (no aubio)
The optional aubio pitch path (ADR 043) turned out to pull ~1–2 GB of
Homebrew deps (the GCC toolchain + openblas + numpy) for one number.
Replaced with an in-process **YIN** f0 estimator over the PCM ffmpeg
already decodes — zero new system dependencies, and pitch is no longer
optional: it works wherever ffmpeg does. Reports mean f0, variability
(std — a second arousal signal), and voiced fraction. YIN is unit-tested
against synthetic tones (recovers 90/150/220/330 Hz within 2 Hz).
ADR 044. 532 → 535 tests.

## [1.5.0] - 2026-07-10

### Added — `analyze_audio_tone`: is the voice message calm or shouting?
`transcribe_audio` gives words, not delivery. `analyze_audio_tone`
measures vocal prosody so you can tell an even, controlled voice from an
agitated/shouting one — a signal a transcript can't carry. One ffmpeg
`astats,ebur128` pass yields the crest factor and the EBU R128 loudness
range (LRA); optional `aubiopitch` adds mean f0. The metric choice
matters: phone voice notes auto-normalize, so absolute loudness is
meaningless — LRA (loudness *spread*) and pitch survive normalization, so
those drive the read (low LRA + steady pitch = controlled; high LRA +
variable pitch = agitated). Reuses the download plumbing; ffmpeg-only
(independent of whisper), degrades to download when ffmpeg is missing;
ships the "proxy, not an emotion model" caveat in every result. ADR 043.
525 → 532 tests.

## [1.4.0] - 2026-07-09

### Added — `set_presence`: flip the dot without touching status
`set_status` can set presence too, but empty status text *clears* the
status — so "go away but keep my current status" meant re-issuing the
whole status with a hand-recomputed expiry (hit in real use: lunch
status up, then wanting the grey dot). `set_presence` (`away` bool,
default true; you-global like `set_status`) calls only
`users.setPresence` and leaves the status alone. `set_status` keeps its
combined AFK path; the two tools split cleanly — "change my status" vs
"just flip the dot." Shares gating with `set_status` (user token,
skipped under `SLACK_READ_ONLY`). ADR 042. 524 → 525 tests.

## [1.3.0] - 2026-07-09

### Added — `view_image`: see image attachments inline
The visual counterpart of `transcribe_audio`. Fetches a message's
`image/*` attachments (screenshots, photos, business cards) with the
server's token and returns them as **inline MCP image content** so the
model sees them directly — a follow-up to the `[🖼 …]` markers digests
already surface. Oversized images (> 6 MB) fall back to a local file
path instead of inlining; under the cap the temp file is removed once
its bytes are in the response. The result leads with a text summary
(count, sizes, skipped non-images) so it reads sensibly even before the
images render. Internally, the audio download plumbing was generalised
(`fetchFiles` / `downloadFiles` / `confinedTempPath` + a temp-filename
`prefix`) — audio filenames stay byte-identical. ADR 041.
520 → 524 tests.

## [1.2.1] - 2026-07-09

### Security (hardening) — confine `download_audio` write paths
Audit prompted by a sibling repo's path-traversal advisory
(yt-mcp GHSA-99mq-fjjc-6v9j). No exploitable equivalent here — the
download source is a Slack-resolved file object, not a caller URL, and
no tool reads-and-exfiltrates a local file. Closed one theoretical gap:
the temp write path sanitized the file name but interpolated the Slack
file ID raw. `confinedAudioPath` now sanitizes BOTH inputs and asserts
the result stays inside `os.TempDir()` (a `filepath.Rel` escape check),
so a write can never land outside the temp dir regardless of Slack's ID
format. Defence-in-depth, byte-identical filenames for real files, no
API change. ADR 040. 519 → 520 tests.

## [1.2.0] - 2026-07-08

### Added — `set_status`: custom status + presence (AFK)
Set/clear your Slack custom status (text + emoji + auto-expiry) and,
optionally, presence (away/auto). Three deliberate choices: (1) a
status is you-global, so empty `workspace` fans out to EVERY workspace
(the opposite of `post_message`) — you're away from all at once;
(2) the server owns the clock — `clear_after_minutes` computes the
expiry timestamp server-side so "AFK till tomorrow" doesn't depend on
the agent guessing wall-time; (3) presence is opt-in (`set_presence`)
and separable from status. Built on a user-token-only `StatusService`;
a bot-only workspace reports "skipped: no user token" instead of a
silent no-op. Registers only with a user token and outside
`SLACK_READ_ONLY`. ADR 039. 514 → 518 tests.

## [1.1.0] - 2026-07-07

### Fixed — `list_users` silently ignored `workspace`
The one reader missed by the ADR 027/029 workspace pass: it declared no
`workspace` argument, so MCP hosts passed the arg through and the tool
listed the PRIMARY workspace regardless. Now it fans out like
`list_channels` (empty = every workspace under `## [label]` headings,
named label = that workspace, unknown = error), and `with_activity`'s
per-user search runs against the correct workspace's index. Additive
argument ⇒ minor release under the ADR 037 contract. ADR 038.
513 → 514 tests.

## [1.0.0] - 2026-07-06

First stable release. **Compatibility contract (ADR 037):** SemVer
governs the machine surface — tool names, argument names/types/defaults,
call semantics, and env-var names. The **rendered output text is
expressly non-contractual** (it targets an LLM, not a parser) and will
keep evolving for density in minor/patch releases — do not regex the
body; use the typed args and structured tokens (permalinks, `cursor:`
ts, issue IDs). Folds in the unreleased 0.6.0 delta-cursor work.

### Added — workspace-aware `get_multi_channel_digest` / `get_morning_recap`
The last two read tools without a `workspace` argument now have one
(empty = every workspace, like the unread sweep). Single-workspace
output is byte-unchanged; multi-workspace nests each under a `## [label]`
heading, with channel resolution kept workspace-local. Closes the
surface asymmetry noted in ADR 034 so the 1.0 read surface is uniform.
ADR 036.

### Added — `get_unread_summary`: `after` delta cursor + `include_refs` gate
Token-effectiveness pass on the highest-traffic tool. Same-day re-pulls
re-emitted ~80% identical content and the always-on References block
(~100 IDs) cost tokens few callers used. Now:
- **`after`** (Slack ts) prunes messages/replies at or before it and
  drops emptied channels — but keeps a channel on a fresh reply to an
  old parent. Applied after the DM/thread-mention merges. Fail-open on
  unparseable ts.
- Each pull emits a `cursor: <newest ts>` header line; feed it back as
  `after` for a self-perpetuating delta loop (no wall-clock tracking).
- **`include_refs`** (default false) gates the trailing References
  block. `RenderReferences` stays one flag away for callers that chain
  into issue IDs.
Purely subtractive, defaults to the cheaper behaviour; existing output
changes only by the one-line `cursor:` header and the now-off refs
footer. ADR 035. 502 → 510 tests. (was staged as 0.6.0)

### Changed — internal: workspace scoping & permalink resolution consolidated
Architecture pass after the 0.5.x run. The single-target scoping triple
(`workspaceTarget → unknown-label check → withClient`, ~10 copies) is
now `scopedWorkspace()`, and the "explicit args win, permalink fills
the gaps" contract (3 copies, with already-drifted error text) is now
`resolveMessageRef()`. `delete_message` keeps its documented
permalink-overrides variant, now explicitly cross-referenced. No
schema or behaviour changes (one error-message normalization in
download/transcribe audio). Registration dual-style (table-driven
pilot vs raw AddTool) reviewed and blessed as cosmetic. ADR 034.
502 → 504 tests.

## [0.5.9] - 2026-07-03

### Added — `transcribe_audio` accepts video: recorded huddles & clips
A recorded huddle (or video clip) is a `video/mp4|webm` attachment on
the same files API — it was rejected only by the `audio/*` filter.
The download loop now takes an accept predicate: `download_audio`
keeps its literal audio-only contract, while `transcribe_audio` widens
to `audio/* + video/*` and passes `-vn` to ffmpeg so video inputs
contribute exactly their audio track. `detectSTT` also picks up
`ffprobe` when present and each transcript header gains the media
duration ("2:13", "1:02:03") so long-call cost is visible before the
text; a missing ffprobe silently omits the duration. Live unrecorded
huddles remain out of scope — no file ever exists. ADR 033.
495 → 502 tests.

## [0.5.8] - 2026-07-03

### Added — `transcribe_audio`: voice message → text in one call
`download_audio` (0.5.7) made the audio reachable but still left the
client to run the STT pipeline by hand. `transcribe_audio` (permalink or
channel + timestamp; `language`, default auto-detect) now runs the whole
chain server-side when a local toolchain is present: download → ffmpeg
(16 kHz mono WAV) → whisper.cpp → transcript. The toolchain is
host-provided and OPTIONAL — resolved via `SLACK_FFMPEG_BIN` /
`SLACK_WHISPER_BIN` / `SLACK_WHISPER_MODEL` with PATH +
`~/.cache/whisper/ggml-small.bin` defaults; when any piece is missing
the tool degrades to `download_audio` behaviour (paths + a setup hint)
instead of failing. stdout/stderr are captured separately so whisper's
model-loading noise can't corrupt the transcript. Transcribed audio
files are cleaned up; failed ones are kept for manual retry. ADR 032.
484 → 494 tests.

## [0.5.7] - 2026-07-03

### Added — `download_audio`: fetch voice-message attachments for transcription
Voice notes rendered as an opaque `[📎 file.m4a]` marker — the audio itself
was unreachable from the MCP client, and fetching it out-of-band would mean
handling the Slack token outside the server process. New `download_audio`
tool (permalink, or channel + timestamp; workspace-aware) resolves the
message, filters attachments to `audio/*`, and streams each into a local
temp file via the server's own token — only file paths cross back to the
client, never credentials. The client then transcribes locally (e.g.
whisper). Service layer: `MessageService.MessageAt` (point lookup with
thread-reply fallback) + `MessageService.DownloadFile`. Downloads are
sniffed for HTML: Slack answers 200 + a sign-in page when the token
lacks `files:read`, which surfaces as an actionable scope error instead
of corrupt "audio". ADR 031. 474 → 484 tests.

## [0.5.6] - 2026-06-30

### Added — `full_text` on `get_thread` + `get_channel_digest`
`MessageLine` truncates long bodies to a compact preview (`+N chars`) —
right for digests, wrong when you need the message verbatim (e.g. ingesting
a thread into a knowledge base, where the most substantive messages were
exactly the ones clipped). The only un-truncated path was
`search_messages full_text`, which can't fetch a specific thread.

Both tools now take `full_text` (bool, default false). Format layer:
`MessageLineFull` (the `MessageLine` logic minus the `MessageLineLimit`
truncation; both delegate to a shared `messageLineImpl`) and a
`WithFullText()` `DigestOption` that also un-truncates reply chains.
Default behaviour unchanged; `get_multi_channel_digest` / `get_morning_recap`
stay compact by design. ADR 030. 472 → 474 tests.

## [0.5.5] - 2026-06-30

### Added — workspace-aware reads + `post_message` dedup
ADR 027 made the writes multi-workspace but left the single-channel/search
**reads** primary-only — so you could *see* a secondary workspace in the
unread sweep but not drill into it. Now `get_channel_digest`,
`search_messages`, and `get_thread` take the optional `workspace` arg (same
`workspaceTarget` helper; empty → primary). Digests/threads/search finally
work against any configured workspace.

`post_message` gains **`skip_if_recent`** (minutes, default 0 = off): if you
already posted the identical text in that channel within the window, the
post is skipped instead of duplicated. Best-effort and fails **open** (needs
a user token to identify self; if it can't, it posts) — and runs per
workspace, so the guard covers non-primary channels too.

Deferred (noted in ADR 029): the channel-list *sweep* tools
(`get_multi_channel_digest`, `get_morning_recap`, `find_decisions`,
`get_user_messages`, `mark_read`, `get_list_items`) — `search_messages
workspace=…` covers the common cross-workspace lookup meanwhile. 471 → 472
tests.

## [0.5.4] - 2026-06-26

### Added — `delete_message` (new write tool, 15 → 16)
Wraps `chat.delete`. The write surface could post but not clean up — so a
couple of `post_message` calls that fired before a "don't post" instruction
left duplicates that had to be removed by hand. Now recoverable from the
server. ADR 028.

- **Smart targeting** — pass either a Slack **permalink** (resolves channel
  + ts in one paste, straight from search/digest output) or **channel +
  timestamp** (e.g. the `ts` returned by `post_message`).
- **Workspace-aware** (mirrors ADR 027): optional `workspace`, defaults to
  primary; confirmation gets the `[label]` suffix only when multi-workspace.
- **Write-gated** under `SLACK_READ_ONLY` alongside `post_message` /
  `add_reaction`. **Irreversible**, but bounded server-side: Slack only
  permits deleting messages this token's identity authored. `deleteErrorHint`
  turns `cant_delete_message` / `message_not_found` into actionable text.

Routing extracted to `runDeleteMessage` so validation + unknown-workspace
paths are unit-tested without a live server. 466 → 471 tests.

## [0.5.3] - 2026-06-24

### Added — channel tools are now workspace-addressable

ADR 023's multi-workspace support only reached the read sweeps; the
write/single-channel tools still hard-targeted the primary workspace, so
there was no way to `post_message` into a secondary one. Now every
channel-addressed tool takes an optional `workspace` argument:

- **`list_channels`** — empty `workspace` lists every workspace in its own
  `## [label]` section (multi only); a named one scopes to it. A
  single-workspace install renders the flat list exactly as before.
- **`post_message`, `add_reaction`, `get_channel_info`, `archive_channel`,
  `unarchive_channel`** — empty `workspace` resolves to the **primary**
  (backward-compatible); a named one routes the call there. Confirmations
  gain a `[label]` suffix only when multiple workspaces are configured.

The asymmetry is deliberate — reads fan out across all workspaces when
unscoped (`workspaceTargets`), writes default to the primary
(`workspaceTarget`): you sweep all inboxes, but you post to one. Routing
for `list_channels` / `post_message` was extracted to `runListChannels` /
`runPostMessage` so the unknown-workspace error paths are unit-tested
without a live server. See ADR 027.

## [0.5.2] - 2026-06-19

### Fixed — digest could overflow the result limit; huddle noise inflated channels

A weekend backlog made `get_unread_summary` render ~57k chars and
**overflow the MCP result-size limit** (spilling to a file). Two fixes:

- **Auto-cap `max_chars`.** Left to default, output is now bounded to a
  total budget (`DefaultTotalMaxChars` = 24000) split across workspaces, so
  a big backlog degrades to the existing "+N channels omitted" footer
  instead of overflowing. `max_chars=0` still means unlimited; a positive
  `N` is still a hard per-workspace cap. (The **absent** case changed from
  unlimited to auto-capped — pass `0` for the old behaviour.)
- **Aggregate huddle noise.** The v0.5.1 huddle fix turned busy standup
  channels into walls of `[huddle]` lines. Content-less huddle pings now
  collapse to a single `· N huddles` line per channel
  (`format.WithHuddleAggregation`); huddles **with replies** stay, and
  **DM** huddles keep rendering inline (the 1:1 call is the signal). A
  huddle-only channel is dropped from the sweep. See ADR 026.

## [0.5.1] - 2026-06-18

### Fixed — huddles rendered as a meaningless `[blocks: 1]`

A Slack **huddle** (audio session) arrives as a block-kit message with
empty text and subtype `huddle_thread`. The empty-body fallback collapsed
it to `[blocks: 1]`, so digests couldn't tell a call had happened — a
23-minute huddle was even reported as "call never connected". Now
`format.IsHuddle` detects the subtype, `renderHiddenPayloadMarker` returns
`[huddle]` (over the generic markers), and `HasContent` never filters a
huddle out. Duration/participants are not shown — slack-go drops the
`room` object (documented follow-up). See ADR 025.

### Tooling — `/mcp` dialog label sync

The `/mcp` dialog labels servers by their **config key** in
`~/.claude.json` (read at session start), not by the `serverInfo.name` the
server reports — so the `"slack v<version>"` from ADR 017 never reached the
dialog (it showed `slack`). Added `scripts/sync-mcp-label.py` + a
`Makefile`: `make install` builds and renames the slk-mcp config key to
`slack v<version>` (matched by binary path, version read from the binary,
idempotent, atomic, `.bak`). A **restart** is required for the dialog to
pick it up (a `/mcp` reconnect is not enough). Rejected hardcoding the
version in the key (goes stale on every bump). See ADR 024.

## [0.5.0] - 2026-06-11

### Added — multiple Slack workspaces in one server

A single Slack token only sees the workspace it was minted in. slk-mcp can
now serve several workspaces at once. Keep your existing `SLACK_TOKEN` /
`SLACK_USER_TOKEN` as the primary and add the rest through a new optional
`SLACK_WORKSPACES` JSON array:

```jsonc
SLACK_WORKSPACES=[{ "name": "secondary", "user_token": "xoxp-...", "bot_token": "xoxb-..." }]
```

- `get_unread_summary` and `get_mentions` now **merge every configured
  workspace automatically**, each under a `## [name]` heading, with a
  `# … — N workspaces` header. Both gained an optional `workspace`
  argument to scope the call to a single label.
- Workspace labels live in the JSON **values** — there are no
  per-workspace environment-variable keys. Adding a workspace never adds
  an env var named after it.
- The optional `SLACK_WORKSPACE_NAME` labels the primary workspace
  (default `primary`).
- Drill-in tools (`get_channel_digest`, `post_message`, `mark_read`, …)
  operate on the primary workspace; writes deliberately stay primary-only.

Single-workspace deployments are unaffected: with no `SLACK_WORKSPACES`
set, the registry collapses to one workspace and output is unchanged.

Internals: `config.WorkspaceViews()` derives a per-workspace `*Config`
sharing global scalars; `slack.NewRegistry` builds one client per
workspace; the `Hub` keeps `h.client` pointed at the primary and retargets
other workspaces through a shallow-copy `withClient` seam, so existing
handlers needed no changes. See ADR 023.

## [0.4.20] - 2026-06-05

### Fixed — bot-mention filter (v0.4.19 fix #2) missed the space-form handle

The v0.4.19 `filterBotSenders` matched automation handles by exact
underscore spelling (`google_calendar`). In production it still let
the Google Calendar ping through: `search.messages` returns that
bot's `Username` as `"google calendar"` — **with a space** — while
the conversations listing uses the underscore. The v0.4.19 unit test
encoded the same wrong assumption (used the underscore form), so it
passed while live behaviour didn't.

Now both the `automationSenders` set keys and the looked-up
`Username` are run through `normalizeSender` (lowercase, strip
` `/`_`/`-`), so every separator spelling folds to one key. The test
asserts the **space** form explicitly, plus title-case/hyphen
variants and a `TestNormalizeSender` table.

ADR 022 records the lesson: normalize external identifiers rather
than enumerate spellings, and test against the form the live API
returns.

## [0.4.19] - 2026-06-04

### Fixed — three digest-usability papercuts (from a week of dogfooding)

**1. DM drill-in was broken (real bug).** `IsChannelID` excluded
`D…` DM IDs, so `get_thread(permalink)` and
`get_channel_digest(channel="D…")` failed with "channel not found"
on any direct message — the permalink-derived DM ID got routed
through `ResolveID` as if it were a channel *name*. New
`IsConversationID` (`C`/`G`/`D`) now drives the `ResolveID`
short-circuit; `IsChannelID` keeps its `C`/`G` channel semantics.
DMs are now first-class drill-in targets.

**2. Automation senders polluted `get_mentions`.** A calendar bot's
"@you Today is …" ping and Slackbot invite notices showed up as
"pending mentions" every day — never actionable. `filterBotSenders`
now drops `google_calendar` / `google_drive` / `slackbot` /
`USLACKBOT` from every mentions sweep.

**3. Empty "(no activity)" stubs.** A channel with no top-level
messages (e.g. a DM pulled in by `dm_window_hours` with only stale
thread replies) rendered an empty stub block in the aggregate
digest. New `WithOmitEmpty` digest option suppresses it in the
unread sweep; single-channel `get_channel_digest` still returns the
informative "(no activity)".

### Notes

No public API or signature changes — `IsConversationID` and
`WithOmitEmpty` are additive, `IsChannelID` is unchanged. The
bot-sender filter is always on (no flag). Tests added for each fix;
full `-race` suite green.

ADR 021 documents all three and explicitly defers a `since`/delta
digest mode as separate future work.

## [0.4.18] - 2026-06-01

### Changed — DMs now outrank non-mention channels in `get_unread_summary`

A 1:1 DM without an explicit `<@you>` mention used to sit in the same
rank band as everything non-mention, ordered by urgency + volume. A
busy bot/log feed (200 messages, many with "error"/"failed" keywords)
could outrank a quiet personal DM and push it below the `max_chars`
cap into the omitted-channels footer.

Observed live: a substantive 1:1 DM was truncated into the footer
while log feeds stayed inlined — the content existed but never
surfaced, and a lone DM line in the footer has no context.

### Change

`RankUnread` gains a DM tier between mention and urgency/volume:

- `mentionBonus = 1_000_000` (explicit mention — unchanged, top)
- `dmBonus = 500_000` (1:1 / mpdm — **new**, above every non-mention channel)
- urgency + volume (the rest, realistically < ~100k)

A plain DM now always inlines ahead of log/git feeds; an explicit
mention still beats a plain DM; a DM that also mentions you stacks
both tiers. DM detection reuses the exported `slack.IsDirectMessage`
(promoted from the v0.4.12 detector), so the channel-ID-prefix
fallback covers DMs whose `IsIM` flag Slack omits — the ones most at
risk of being dropped.

### Tests

4 new ranker tests: DM outranks a 200-message keyword-heavy channel;
mention outranks a plain DM; DM+mention tops both; a flag-missing
`D…`-prefix DM still gets the tier. Full `-race` suite green.

ADR 020 documents the tier arithmetic and the detector reuse.

## [0.4.17] - 2026-05-29

### Fixed — `parent_test.go` flake, for real this time (deterministic ticker)

`TestWatchParent_NilLoggerSafe` flaked on the v0.4.16 Go 1.24 `-race`
CI job (5s deadline hit; same SHA passed on the main run). This is
the third flake in this family after ADR 012 (deadline 2s→5s) and
ADR 015 (interval 1ms→10ms). Both prior fixes tuned a real-time
constant against a real `time.NewTicker`; neither could survive a
CI runner that stalls a goroutine past any fixed deadline.

### Root cause(s)

Two bugs, one masking the other:

1. **Real-time dependence.** The tests asserted a wall-clock deadline
   against a real ticker under `-race`. Inherently flaky on a loaded
   shared runner.
2. **A latent harness race the timing noise hid.** `pidSource.get()`
   closed its `initialised` channel *before* reading the value, so
   `waitInitialised` could release the test to `Store` a new pid
   while the watcher's initial `Load` was still pending. If the store
   won, `initial` captured the new pid, the change was never
   detected, and `onLost` never fired. Reproduced ~1 in 50 `-race`
   runs once the ticker noise was removed.

### Change

- `parent.go`: added an unexported `tickerFunc` seam. Public
  `WatchParent` wraps `watchParent(..., newRealTicker)`; tests inject
  a manually-driven fake. **Public API and runtime behaviour
  unchanged**; `main.go` untouched.
- `parent_test.go`: all tests now advance the watcher via an
  unbuffered `tick()` (blocks until the poll is consumed) instead of
  racing the wall clock. `testDeadline` / `testPollInterval` remain
  only as a backstop / nominal value, no longer gating correctness.
- `pidSource.get()` now snapshots the value before signalling
  initialisation, closing the race in (2).
- Two assertions newly possible with the seam: the zero-interval
  default now asserts `DefaultParentPollInterval`, and a new
  `TestWatchParent_StopsTickerOnReturn` verifies ticker cleanup.

### Verification

- `go test -race -count=200 ./internal/lifecycle/...` — 1400/1400.
- `go test -race -count=1 ./...` ×3 — green.

ADR 019 documents the move from constant-tuning to deterministic
injection and supersedes the *approach* (not the constants) of
ADR 012 and ADR 015.

## [0.4.16] - 2026-05-29

### Added — `get_list_items` tool for Slack Lists

Slack Lists (the new structured-table feature surfaced under
`https://<workspace>.slack.com/lists/<team_id>/<F-id>`) was not
accessible from slk-mcp. List rows live behind the
`slackLists.items.list` endpoint, which slack-go v0.15.0 does not
wrap. This release adds a thin first-class surface for reading them.

### Change

- New `internal/slack/lists.go` with `ListService`. Speaks raw HTTP
  to `slackLists.items.list` (POST, JSON body, Bearer auth) because
  the underlying SDK has no helper. Configurable `BaseURL` /
  `Endpoint` make the service fully testable via `httptest`.
- New `internal/tools/lists.go` registering `get_list_items` via the
  table-driven shape. Gated on `RequiresUserToken` — `lists:read`
  is denied on bot tokens, so the tool refuses to register without
  a user token rather than failing with `missing_scope` at runtime.
- Defensive decoder: each Slack-side item is kept as a `map[string]any`
  alongside extracted fields, and a `bestEffortTitle` heuristic picks
  the most title-like cell so single-column lists render readably
  without the caller knowing the schema.
- 429 rate-limit responses are wrapped in `goslack.RateLimitedError`
  so a future `ratelimit.DoR` wrap retries transparently. Today the
  caller surfaces the error and re-issues — Lists API is low-volume,
  retry-in-loop would be over-engineered.

### Operator action required

Slack OAuth scope **`lists:read`** must be added to the app
installation backing `SLACK_USER_TOKEN`. After re-authorization, the
new xoxp- token replaces the old in `~/.claude.json` under
`mcpServers.slack.env.SLACK_USER_TOKEN`. Without the scope the tool
returns Slack's verbatim `missing_scope` error so the fix is
unambiguous.

### Tests

- 8 new tests in `internal/slack/lists_test.go` cover: HasToken
  guard, missing-token early return, missing list_id rejection,
  happy path (item parsing + cursor + title heuristic), pagination
  parameter forwarding, `missing_scope` surfacing, 429 →
  `RateLimitedError`, malformed-body fragment in error, table-driven
  `displayValue` + `bestEffortTitle` coverage, `parseRetryAfterSeconds`
  edge cases.
- Suite size 403 → 421 (`go test -race -count=1 ./...`).

ADR 018 documents the raw-HTTP decision and the scope-gating
contract.

## [0.4.15] - 2026-05-28

### Changed — self-reported server name embeds the version

The first argument to `server.NewMCPServer` is the server's
self-reported name, surfaced by MCP hosts (Claude Code's `/mcp`
listing, error banners, IDE tool-call prefixes). It was the bare
string `"slack"`, so the host had no way to show which version was
actually running without a separate probe — annoying after a rebuild
when verifying the new binary is the one the host reconnected to.

Now the name carries the version: `"slack v0.4.15"` (etc.). One
glance at `/mcp` reveals what's loaded. No new tools, no extra
calls, no host-side configuration.

### Caveat

If callers had been keying on the *self-reported* name (e.g. host
allow/deny lists that match `serverInfo.name`), they would need to
match a prefix (`slack v*`) instead of an exact `slack`. In practice
the keys MCP hosts maintain are the *config-side* IDs (the JSON key
under `mcpServers`), which are unaffected — no breakage observed.

ADR 017 documents the tradeoff.

## [0.4.14] - 2026-05-28

### Fixed — `get_mentions(pending_only=true)` thread-reply false positive

`pending_only` flagged a mention as pending even when the operator
had already replied — provided that reply landed inside a thread
rather than at the top level of the channel/DM. The detector used
`conversations.history` only, which returns top-level messages and
deliberately omits thread replies; an in-thread answer was therefore
invisible to it.

This shipped as a silent false positive: surfaced "open" items the
operator had already handled, and on a busy day pushed real
unanswered asks down the list.

### Root cause

`operatorReplied` ran a single `conversations.history` scan with
`Oldest = mention.ts` and looked for any newer message authored by
the operator. Two classes of in-thread replies fell through:

1. **Mention is itself a thread reply.** Its `permalink` carries
   `?thread_ts=<root>` and any operator follow-up lives in the same
   thread. `conversations.history` returns only the thread root, not
   the replies — so the operator's text answer is invisible to the
   scan.
2. **Mention is top-level and the operator answered by opening a
   thread on it.** Same omission, mirror direction.

### Change

- New helper `operatorRepliedSince(ctx, msgs, log, m, selfID)` runs a
  two-stage check: top-level `History` first (short-circuits on hit
  to avoid the extra call), then `ThreadReplies` against the
  thread root extracted from the mention's permalink. The root falls
  back to the mention's own timestamp, so the second pass also
  catches the "operator opened a thread on the mention" case.
- `Hub.operatorReplied` is now a one-line wrapper, keeping the
  filter-pipeline call sites unchanged.
- 8 new tests in `pending_reply_test.go` cover: top-level reply
  short-circuit, both in-thread cases, none-anywhere baseline, older
  reply chronology guard, empty-text reply guard, history-error
  fallthrough to thread sweep, and empty-selfID early exit.
- Worst-case API cost per mention is now 2 calls (history + replies)
  instead of 1. `pending_only` is already an opt-in expensive filter
  and the worker pool + ratelimit wrapper bound the throughput, so
  the extra call is well within budget.

ADR 016 documents the call shape and the reason for not gating the
thread sweep on permalink parsing alone.

## [0.4.13] - 2026-05-27

### Fixed — `parent_test.go` flake (deeper fix after v0.4.10)

v0.4.10 bumped `testDeadline` 2s → 5s thinking the deadline was too
tight. The flake came back on the next CI run anyway: the test
budget was a red herring — the real bottleneck was the **poll
interval**.

The watcher uses `time.NewTicker(interval)`; tests passed
`1 * time.Millisecond`. Under the race detector on a loaded CI
runner that interval is **below kernel scheduler granularity**
(~4ms typical under stress) AND amplified by the race detector's
goroutine-scheduling memory barriers. Net effect: the 1ms ticker
sometimes didn't fire at all within 5 seconds, and the deadline
bump just delayed the inevitable.

### Change
- New `testPollInterval = 10 * time.Millisecond` constant in
  `parent_test.go` with a long rationale comment.
- All 5 `1 * time.Millisecond` / `10*time.Millisecond` literals in
  the test file replaced with the constant.
- Production code (`WatchParent`, the lifecycle semantics) untouched.

### Effect
Happy-path latency unchanged in human terms (waitInitialised + 1
tick + onLost ≈ 30ms typical). With `testDeadline = 5s` the budget
now covers 500+ ticks of headroom — plenty for any real
ppid-change detection.

ADR 015 documents the lesson: **deadline budgets compensate for
slow ticks, not absent ticks. The interval is the lever that
matters under -race on stressed runners.**

## [0.4.12] - 2026-05-26

### Fixed — DM-window silent-miss bug

Observed in production: an outgoing message the operator just sent
to a DM (no incoming reply yet) didn't surface in
`get_unread_summary(dm_window_hours=12)` even though it was well
within the time window. Two cooperating root causes:

1. **`mergeDMOverride`** required `b.Channel.IsIM || b.Channel.IsMpIM`
   on the *base* entry before letting an override replace it. Slack's
   `users.conversations` doesn't always populate those flags for
   read-state-stale DMs (typical for outgoing-only) — so the
   conditional rejected a legitimate replacement and the truncated
   unread-only view persisted.
2. **`RecentDMActivity` worker filter** used the same brittle
   `ch.IsIM || ch.IsMpIM` check, so a DM with the flags missing was
   silently skipped before the history fetch could run.

### What changed

- Dropped the `IsIM/IsMpIM` conditional in `mergeDMOverride`. If the
  override has a matching channel ID, replace — the override side
  has already filtered to DMs via `isDirectMessage`.
- New `slack.isDirectMessage(ch)` helper. Trusts `IsIM`/`IsMpIM`
  when set; falls back to channel-ID prefix (`D…` for IM, `G…` +
  `mpdm-` name for MPIM). `G…` channels with non-mpdm names stay
  treated as private groups, not DMs.
- `RecentDMActivity` worker now calls `isDirectMessage(ch)` instead
  of inline boolean checks.

### Why
ADR 014 documents the rationale. The fix is purely a robustness
upgrade — no API contract change, no new parameters, no surface
growth.

## [0.4.11] - 2026-05-26

### Added — `archive_channel` and `unarchive_channel`

The workspace-audit flow from v0.4.9 (`list_channels(unjoined_only=true)`)
surfaces dead / orphan channels but had no follow-up to clean them
up — the operator had to switch to the Slack UI. Two new write
tools close that loop:

- **`archive_channel(channel)`** — wraps Slack's
  `conversations.archive`. Reversible; Slack hides the channel
  from active lists and rejects new messages but does not
  permanently delete it.
- **`unarchive_channel(channel)`** — symmetric restore via
  `conversations.unarchive`.

Both accept either a channel name (`#general`, `general`) or a
canonical channel ID (`C0ABC1234DE`) — they thread through
`ChannelService.ResolveID`, which already short-circuits on
canonical IDs (v0.4.6).

Permissions: the user token needs `channels:manage` for public
channels and `groups:write` for private ones.

### Why
Both tools are *write* operations and so are gated by
`SLACK_READ_ONLY` and `IsDisabled(name)` checks alongside
`post_message`, `add_reaction`, and `mark_read`. A read-only
deployment never sees them.

`ChannelClient` contract grows by `Archive` and `Unarchive` —
matching the established pattern. The compile-time assertion in
`contracts.go` continues to enforce that `*slack.ChannelService`
satisfies the broader interface.

ADR 013 documents the rationale.

## [0.4.10] - 2026-05-26

### Fixed — CI flake in `internal/lifecycle/parent_test.go`

`TestWatchParent_FiresWhenPpidChanges` flaked intermittently on Go
1.23 `-race` runs (same commit passed on the dev branch CI, failed
on main). Root cause: every async assertion in the file used a
hard-coded `2 * time.Second` deadline, which is tight when the race
detector adds ~5–10x goroutine scheduling overhead on a shared CI
runner.

- Extracted `testDeadline = 5 * time.Second` constant at the top
  of `parent_test.go` with a comment explaining the rationale.
- Replaced every `time.After(2 * time.Second)` (×7) and
  `time.After(time.Second)` (×1) with `time.After(testDeadline)`.
- Updated the one stale failure message that hardcoded "within
  2s" to read "within deadline".

### Why
Same SHA producing different CI results between dev and main was a
schedule artefact, not a code regression. The fix raises the
ceiling for a real hang to surface (5s is still a generous bound)
without changing any production logic. ADR 012 documents the
rationale.

## [0.4.9] - 2026-05-26

### Added — channel audit on `list_channels`

The previous `list_channels` output named channels and showed member
counts but gave no signal about whether the operator was already a
member — and no surface for "find me the channels I'm NOT in yet."
That broke the workspace-audit use case.

- New optional `unjoined_only: bool` (default `false`). When `true`,
  the result is filtered to channels where `IsMember == false` —
  the primary audit case.
- Each rendered line now marks `[NOT JOINED]` for non-member
  channels and `🔒` for private channels. Joined public channels
  stay quiet (loud-on-anomaly, silent-on-common-case).
- Context falls back from `Topic` → `Purpose` so a channel with no
  topic but a real purpose still carries a description.
- Tool description rewritten to call out the audit use case
  explicitly so LLM consumers reach for `unjoined_only=true` when
  asked "which channels haven't I joined."

### Why
ADR 011 documents the rationale. The fix exposes existing Slack
`conversations.list` fields (`IsMember`, `IsPrivate`, `Purpose`)
that the previous renderer dropped — no new API calls, no contract
break.

## [0.4.8] - 2026-05-25

### Fixed — thread-mention backstop on `get_unread_summary`

Silent-miss bug: when a teammate tagged the operator in a reply to a
thread whose parent was already read, Slack delivered the
notification but the unread sweep dropped the channel entirely.
Cause: `UnreadService.fetchReplies` only iterates new top-level
messages, so a reply to an *old* thread never enters `cu.Replies`,
and `ChannelMentions` returns false for the channel.

- New `UnreadService.UnreadThreadMentions(ctx, hours)` calls Slack's
  `search.messages` with `to:me after:<date>`, groups hits by
  channel, and parses each hit's `?thread_ts=` from its permalink
  to attach it under the right thread bucket. Filters hits to the
  exact time window (Slack's `after:` is date-granular only).
- New optional `thread_mention_hours: int` on `get_unread_summary`
  (default `24`). When `> 0`, the handler calls the backstop and
  merges the result via `mergeThreadMentions(base, mentions)`. New
  channels are appended; existing channels gain the mention reply
  in their `Replies[threadTS]` bucket. Dedup by message timestamp.

### Why
ADR 010 documents the rationale. The fix is at the service layer
(no new tool), so `mentions_only`, `get_unread_summary`, and
downstream urgency ranking all start surfacing thread-reply
mentions in the same call.

## [0.4.7] - 2026-05-21

### Added — DM time-window override on `get_unread_summary`

Daily recaps missed conversations the operator had themselves
participated in: any DM or multi-party DM read in the Slack UI no
longer surfaces in the unread sweep, even when the messages contain
decisions the user cares about (executive sync, side-chat handoffs).

- New optional `dm_window_hours: int` (default `0` = disabled,
  non-breaking). When `> 0`, the handler also calls a new
  `UnreadService.RecentDMActivity(ctx, hours, maxPerChannel)` and
  merges the result on top of `UnreadAll`. DM and multi-party-DM
  entries from the time-window fetch replace the unread-only
  versions; new DMs that weren't in `UnreadAll` (because they were
  already read) get appended.
- `RecentDMActivity` lists joined channels via the existing
  `JoinedChannels` helper (already configured for `im` + `mpim`),
  filters to DM types, and pulls `conversations.history` since
  `now − hours`. Reuses `fetchReplies` so the thread-reply contract
  matches `UnreadAll` exactly.
- Time source is a package-level `nowUnixFn` seam so tests can pin
  the cutoff deterministically without mocking the system clock.

### Why
ADR 009 documents the rationale: unread state is the wrong primitive
for end-of-day recaps when the operator is themselves a participant.
The new flag preserves the unread default for normal scans while
making DM-rich windows reachable with one parameter.

## [0.4.6] - 2026-05-21

### Fixed

- **`get_thread` and `mark_read` permalink callers no longer fail with
  "channel #C0... not found"**. `slack.ChannelService.ResolveID` now
  short-circuits when the input is already a canonical Slack channel
  ID (`C…` / `G…` per `IsChannelID`) and returns it verbatim. Callers
  that pass a permalink-derived `p.ChannelID` straight through the
  same code path are no longer rejected as if the ID were a channel
  *name*.
- **`MessageLine` no longer renders an effectively empty line for
  messages that carry only legacy `Attachments` or Block Kit
  `Blocks`**. When body text and `Files` are both empty, the
  renderer appends a short marker (`[attached: N]` /
  `[blocks: N]`) so the reader knows there is a non-text payload
  reachable via the permalink. URL-preview messages (text + an
  Attachment) stay clean — the branch only fires when body == "".

### Why

`get_thread(permalink=...)` failed in practice on this workspace
because Slack permalinks embed a channel ID, not a name, and
`ResolveID` treated every input as a name. The fix is at the
service layer so every caller — `get_thread`, `mark_read`, and any
future consumer — gets the right behaviour without per-handler
plumbing.

The `MessageLine` issue surfaced when a thread parent was posted
with content in `Attachments` only (forwarded message, integration
post) — the rendered line had a timestamp, an author, then nothing.
A reader couldn't tell whether the user actually posted an empty
message or whether the renderer dropped a payload. The new marker
makes the distinction explicit.

See ADR 008.

## [0.4.5] - 2026-05-21

### Added — `with_thread_context` on `get_user_messages`

LLM-consumer pain point: a search hit like `"ok"` or `"got it"` is
impossible to interpret without the parent it was replying to. Slack
search returns the hit body in isolation; there was no first-class
way to drill into the parent without an extra manual `get_thread`
call per hit.

- New optional `with_thread_context: bool` (default `false`,
  non-breaking). When set, the handler identifies every hit that is
  a thread reply (`thread_ts != ts`), batches one
  `conversations.replies` call per unique thread (deduped via the new
  `threadKey` helper), and inlines the parent on a continuation line
  beneath each hit:

  ```
  - #team-alpha 2026-05-20 11:47 (alice) got it, will do
      ↑ [10:11 bob] please ship the fix today
  ```

- New `format.ExtractThreadTS` (renamed from private
  `extractThreadTS`) and `format.ThreadContextLine` exported so the
  tools package can render the indented continuation line in the
  same style as the rest of the digest output.

### Why
Slack's `search.messages` doesn't include thread-parent context.
For private channels where the conversation IS the context (chats
between leads, back-and-forth in restricted channels), single-line
hits are nearly useless. The new flag turns one cheap opt-in into a
full-fidelity readout. See ADR 007.

## [0.4.4] - 2026-05-15

### Added — `get_unread_summary` output-size controls

LLM-consumer pain point: a workspace with ~45 unread channels produced
a 55K-char digest, blowing past per-tool token caps even though the
ranking pipeline already knew which channels mattered most. Three
additive parameters now let callers cap the output without losing the
ranking signal:

- **`max_chars`** (default `0` = unlimited) — soft cap on rendered
  body size. Channels are emitted in urgency order until the cap is
  reached; the rest are listed in a footer (`+ N channels omitted by
  max_chars cap: …`) so the caller can drill in via
  `get_channel_digest`. Iteration uses `continue`, not `break`, so a
  smaller lower-urgency channel can still fit after a larger one is
  rejected.
- **`skip_log_mode`** (default `false`) — omit `[LOG MODE]` channels
  entirely (alert / error feeds).
- **`skip_git_mode`** (default `false`) — omit `[GIT MODE]` channels
  entirely (CI / git-bot feeds).

### Changed

- `log_samples_per_band` default lowered from `3` → `1`. The samples
  for INFO bands rarely added signal and dominated long log
  channels. Callers who want the previous behaviour can pass
  `log_samples_per_band: 3` explicitly.

### Why
ADR 006 documents the rationale: the existing urgency ranking
already knew which channels mattered most, but downstream filtering
ignored it once size became the constraint. The new flags turn
ranking into a budget, not just an ordering signal.

## [0.4.3] - 2026-05-12

### Changed — handlers now consume the interface seam

- Every handler call against the Slack service layer was rewritten
  from `h.client.X.Method(...)` to `h.X().Method(...)` (33 sites).
  Production code now exercises the `UserClient` / `ChannelClient`
  / `MessageClient` / `SearchClient` / `UnreadClient` contracts
  declared in `contracts.go`, instead of the concrete services.
- `channelDisplayLabel` broadened from `*slack.UserService` to
  `UserClient`; `Name(ctx, id) string` added to `UserClient` to
  support that consumer.
- `registerSearchTools` migrated to the `Hub.register(s, toolDef{...})`
  table-driven shape; `handleSearchMessages` / `handleFindDecisions`
  extracted as named methods. Other register* methods continue in
  their current shape; `search.go` is the reference for incremental
  migration.
- `//nolint:unused` directives on `toolDef` / `register` / `wrap`
  were removed; the seam is now load-bearing.

### Fixed — sensitive-data hygiene

- `internal/format/format_test.go` swapped two real workspace
  channel names for synthetic placeholders (`team-alpha`,
  `team-bravo`). Behaviour identical; no token/hostname change.

### Why
ADR 005 documents the rationale: both the `Hub.X()` accessors and
the `toolDef` table seam shipped in v0.4.0/v0.4.1 as design intent
without a real consumer. Migration earns them their keep before they
silently rot.

## [0.4.2] - 2026-05-12

### Added

- `get_user_messages` now accepts optional `since` / `until` (YYYY-MM-DD)
  parameters that map straight to Slack's `after:` / `before:` search
  operators. One call answers "did user X post in channel Y between
  these dates?" deterministically — independent of the caller's
  unread / `last_read` state.
- Tool description spells out the preferred-over-`get_unread_summary`
  use case so deadline-style queries route to the right primitive.

### Why
Read-state-driven tools (`get_unread_summary`) silently omit posts
the caller already saw — leading to false "no post today" inferences
when the question is really "did the post exist." Absolute-time
scans are immune to that confusion. See ADR 004.

## [0.4.1] - 2026-05-12

### Added — infra, no behaviour change.

#### CI hardening (`.github/workflows/test.yml` + `.golangci.yml`)
- Build / vet / test now run on a `go: ['1.23', '1.24']` matrix.
  `fail-fast: false` so a regression on either version is visible.
- Tests run with `-race` — the channel/user caches are RW-locked and
  digest fan-out uses worker pools; the detector catches drift
  cheaply.
- New `lint` job runs `golangci-lint` with a deliberately narrow rule
  set: `errcheck`, `govet`, `staticcheck`, `ineffassign`, `unused`,
  `misspell`. Style-only linters are intentionally excluded — we
  block CI on correctness, not aesthetics.

#### Architecture Decision Records (`docs/adr/`)
Three retroactive ADRs capturing the non-obvious decisions behind
recent versions:
- **001** — GIT MODE prefers MR-iid over issue-id (v0.3.24).
- **002** — Unified id→name refs map for users and channels (v0.3.26).
- **003** — `Hub` receiver replaces `Deps` service-locator (v0.4.0).

Each ADR records the context, the rejected alternatives, and the
trade-offs. Format and "when to write one" guidance in `docs/adr/README.md`.

#### Interface seam at the tools ↔ slack boundary (`internal/tools/contracts.go`)
Narrow consumer-side interfaces — `UserClient`, `ChannelClient`,
`MessageClient`, `SearchClient`, `UnreadClient` — declared at the
tools package boundary. The concrete `*slack.XService` types satisfy
them implicitly; compile-time assertions enforce that drift breaks
the build with a clear `does not implement` diagnostic.

Hub gains accessor methods (`Users()`, `Channels()`, `Messages()`,
`Search()`, `Unread()`) that return these contracts. New handler
code SHOULD call `h.Users().X()` instead of `h.client.Users.X()` so
future tests can substitute fakes via a wrapper-Hub composition.
Existing handlers continue to work unchanged; migration is gradual,
not blocking.

### Quality
323 tests pass across 9 packages, race detector clean, vet clean,
sensitive-data scan clean.

## [0.4.0] - 2026-05-12

### Architecture refresh — no behaviour change, no tool-surface change.

Same MCP contract, same outputs. The internals were reshaped so the
package boundaries match what each layer is actually doing.

#### Package split (PR-1)
`internal/tools/` had drifted to 2948 LoC mixing MCP-handler wiring
with pure rendering and classification logic. Split:

- New `internal/digest/` package — pure helpers, no `mcp.*` types,
  no shared-state struct: `dedup`, `gitchannel`, `logchannel`,
  `lowsignal`, `refs`, `urgency`, `zabbix` (Slack-channel alert
  parser, despite the name), plus the `RankUnread` / `ChannelMentions`
  scoring previously buried in unread.go.
- `internal/slack/permalink.go` — `ParseSlackPermalink` belongs at
  the Slack-protocol boundary, not in the MCP wiring layer.
- `internal/tools/` is now 1797 LoC, exclusively MCP-handler concerns.

#### `Hub` receiver pattern (PR-2)
`tools.Deps`-as-service-locator replaced by a `tools.Hub` that owns
the slack client, config, and structured logger. main.go:

    tools.NewHub(client, cfg, log).RegisterAll(mcpServer)

All register* functions and their helpers (resolveTargetChannels,
resolveRefs, resolveRefsWithReplies, filterPendingMentions,
operatorReplied, fetchMentionContext, fetchLastPostDates,
channelDigest, channelDigestRange) are now methods on `*Hub`. Pure
helpers (parseChannelList, collectUserIDs, mergeRefs, detectDecisions,
matchDecision) stay as free functions.

Introduced `toolDef` + `(h *Hub).register(s, defs...)` + `wrap()`
middleware seam. Today the seam is a pass-through — the hook is
in place for future timing / panic recovery / structured logs
without touching individual handlers.

#### Generic retry (PR-3)
New `ratelimit.DoR[T any](ctx, log, fn func() (T, error)) (T, error)`
collapses the recurring three-line "var x; ratelimit.Do { x = r }"
glue to one line. Wired through every single-value Slack API call
in `slack/channels.go`, `messages.go`, `search.go`, `users.go`.
Multi-step / void-return paths keep `Do`.

#### File-size cap (≤ 600 LoC)
`internal/tools/unread.go` (was 641 LoC) split into:
- `unread.go` (275 LoC) — handler registration only.
- `unread_helpers.go` (340 LoC) — filter*, fetchMentionContext,
  channelDisplayLabel, resolveRefsWithReplies, collect*.

No source file in the repo now exceeds 600 LoC.

### Quality
323 tests pass across 9 packages, `go vet` clean, sensitive-data
scan clean.

## [0.3.26] - 2026-05-12

### Fixed
- **`<#CHANNELID>` references in message bodies are no longer rendered as raw `<#C0ABC1234DE>` markup.** `RenderText` now resolves channel references the same way it resolves user mentions: prefers the inline pipe label (`<#CID|name>` → `#name`); falls back to a reverse `id→name` lookup populated from the channel cache; emits `#CID` as a last resort instead of dropping the reference. Channel digests that quote `<#CID>` now render the resolved `#name` rather than the opaque ID.

### Added
- `slack.ChannelService.NamesForIDs(ctx, ids)` — batch reverse lookup, hits an internal `idCache` first (populated by every `ResolveID` / `List` / `Info` call) with `conversations.info` fallback for unseen IDs. Mirrors `UserService.NamesFor`.
- `slack.IsChannelID(string) bool` — detects canonical Slack channel IDs (`C…` public, `G…` private; `D…` DMs intentionally excluded).
- `format.CollectMentionedChannelIDs(messages)` — sibling of `CollectMentionedUserIDs`.
- `tools.resolveRefs(ctx, d, messages)` and `tools.resolveRefsWithReplies(ctx, d, cu)` — unified id→name builders that merge user and channel resolutions into a single map (Slack ID prefixes keep the namespaces disjoint).
- `get_channel_info` now accepts a Slack channel ID directly (`C0ABC1234DE`) alongside a channel name — handy for resolving a `<#CID>` reference surfaced by another tool without an intermediate lookup step.

### Changed
- `RenderText`'s second parameter is now semantically a merged id→name map for users **and** channels. The existing user-only call sites continue to work; channel resolution only kicks in for callers that pre-merge channel names (done internally by `resolveRefs` / `resolveRefsWithReplies`).

## [0.3.25] - 2026-05-07

### Added
- `get_thread` and `mark_read` accept a Slack `permalink` argument as an alternative to (channel + thread_ts / timestamp). Permalink-only callers no longer have to parse the URL themselves; explicit args still win when both are provided. Thread-reply permalinks correctly extract the thread root via the `thread_ts` query parameter for `get_thread`, and the message's own ts for `mark_read` — they are different intents.
- `internal/tools/permalink.go`: shared `parseSlackPermalink` helper. Returns `(channel_id, ts, thread_ts)` or `errNotASlackPermalink` for inputs that look like URLs but lack the channel / "p<ts>" segments. Empty input is a no-op so callers can treat "no permalink" as "no override".

### Fixed
- GIT MODE: `→ →` between deploy verbs ("deploy → → deploy ✓") collapsed to a single arrow. `joinVerbs` now elides the separator when the previous verb already ends with `→`.
- GIT MODE: trailing `— —` segment dropped when a workflow has no parseable actors (typical for deploy / pipeline events). Saves a few tokens and reads cleanly.

## [0.3.24] - 2026-05-07

### Fixed
- **GIT MODE: ticket misattribution.** The workflow grouper picked the first `XXX-NNN`-style ID it saw in a bot message, which often came from a branch name and disagreed with the MR title (e.g. an MR about ticket A delivered on a branch named after ticket B was labelled with ticket B). Workflow keys now prefer the MR-iid (`!1234`) when present, so the canonical identity matches the MR itself; ticket IDs in branches no longer override it.
- **GIT MODE: branch lifecycle events split from their MR.** "branch new" / "branch rm" events appeared as separate workflows from the merge they belonged to (e.g. a `localization` branch and `!937` showed up as two stories about the same change). Added a pre-pass that records branch ↔ MR-iid pairs observed in any single message, then collates branch-only events under the linked MR.
- **GIT MODE: author / reviewer / merger flattened into one actor list.** The renderer joined every actor with `/`, conflating the MR author with reviewers and the merger. Verbs now imply a role (`MR open` → author, `approved` → reviewer, `merged` → merger), and the rendered actor list tags structured roles inline (`alice(author/merger) bob(reviewer)`). Plain-actor verbs (push, branch ops, deploy, pipeline) stay un-tagged.

### Tests
- Added regressions for: MR-iid priority over issue ID; branch alias collation; role tracking when one actor wears two hats; MRs without any ticket prefix in title.

## [0.3.23] - 2026-05-05

### Fixed
- `search_messages` body was hard-truncated at 200 chars with no opt-out, swallowing issue IDs and URLs that landed at the end. Now there's a `full_text` flag (default false to preserve token-thrift) that disables truncation when callers know they need the tail.

### Added
- `search_messages` hits now carry a `thread_ts=… <permalink>` continuation line. For top-level messages thread_ts equals the message ts; for threaded replies it's parsed out of the Slack permalink. This lets the LLM chain straight into `get_thread` without re-searching.
- `get_channel_digest` accepts `after` / `before` (YYYY-MM-DD, UTC). When set, they override the relative `hours` window — useful for post-mortem reconstructions ("dump #team-alpha between 2026-04-30 and 2026-05-01") that fuzzy search semantics don't reliably cover.

## [0.3.22] - 2026-05-05

### Added
- `list_users` accepts `filter` (case-insensitive substring) — matches against handle, real name, display name, and job title. Lets the LLM narrow to "marketing" / "qa" / "devops" without rendering the full 80+ user dump.
- `list_users` now renders `profile.title` (job title) as a column. Slack stored it all along; we just weren't surfacing it. Critical for "who's on team X" queries when channel membership is fuzzy.

## [0.3.21] - 2026-05-05

### Fixed
- `get_channel_info` returned `members: 0` for every channel because Slack's `conversations.info` omits `num_members` unless you pass `include_num_members=true`. The wrapper now always sets it, matching what `list_channels` reports.

### Added
- `get_channel_info` accepts `include_members` (bool) and `members_limit` (int, default 50). When enabled, fetches the channel roster via `conversations.members` and renders display names — useful for "how many people on the X team" lookups without needing to read the channel.

## [0.3.20] - 2026-05-04

### Fixed
- `pending_only=true` now skips mentions whose body is empty. An empty message can't be "waiting for a reply" — there was nothing to reply to. Empty-body matches were a false-positive source.

### Added
- `strict_mention` (bool, default false) — when true, drops matches that don't literally contain `<@SELFID>` (or `<@SELFID|name>`) in the message body. Filters Slack-search false positives in shared channels where you're a member but were never directly tagged.
- `drop_closing_acks` (bool, default false) — when true, drops mentions whose body is a short closing acknowledgement (`thanks`, `спасибо`, `ok`, `+1`, `got it` and similar in en/ru). Useful with `pending_only=true` to avoid surfacing already-closed conversations.



### Added
- Message rendering now surfaces file attachments. Images get `[🖼 name (WxH)]`, other files get `[📎 name]`. Previously these were silently dropped, hiding screenshots and other attachment-only context from the digest.
- `format.HasContent` treats messages with attachments as content-ful (so a screenshot-only message no longer gets filtered out as empty).

## [0.3.18] - 2026-05-04

### Fixed
- `pending_only=true` now returns an error when `auth.test` failed (previously silently passed every match through).
- `classifyLogSeverity` no longer bins success reports as ERROR just because they contain the literal "Failed: 0". When the body has both a "Status: PASSED" / "Pass rate: 100%" marker AND a `failed: 0` line, classify as INFO. Cuts log-mode noise on healthy CI feeds.

### Changed
- `get_mentions(with_context=true)` deduplicates context messages across consecutive same-channel mentions: each (channel, ts) shown at most once. Saves ~30–40% on mention sections dominated by one chatty thread.
- Context messages with no signal (empty body, no reactions, no replies) are filtered out.
- Channels detected as "low-signal" (name keyword OR ≥5 messages with average body length under 16 chars and no thread replies) collapse to a single line: `## #name — N short status updates from M people (...)`.



### Added
- `get_mentions` gains `pending_only` (bool, default false). When true, each match is checked against `conversations.history` after the mention timestamp; only mentions where the operator hasn't posted a non-empty text reply are kept. Reactions and empty messages don't count as a reply, so emoji-only "acks" still surface as pending. One history call per match (4-worker pool).

## [0.3.16] - 2026-05-04

### Added
- `get_unread_summary` now ends with a `## References` footer that lists every issue ID, MR number, and branch name referenced anywhere in the digest, deduplicated and sorted. Designed as a hand-off to enrichment MCPs (issue trackers, code-review tools, dashboards) so the orchestrator can batch-call them without re-parsing prose. slk-mcp stays product-agnostic — the same footer works for any external system that takes one of those identifier shapes.

## [0.3.15] - 2026-05-04

### Added
- Zabbix-style alerts in log channels are parsed into a structured one-liner: `State: Host — Trigger (sev X) [opdata]`. Multi-line label/value payloads (Host, Severity, Opdata, Trigger description) collapse into a single readable line. Known opdata patterns are compacted (`Load averages(...): (a b c), # of CPUs: N` → `load5=b, CPUs=N`; `Space used: A of B (P %)` → `P% (A of B)`). Unknown opdata passes through truncated to ~80 chars.
- The structured output gives the LLM (and operator) enough host + metric context to decide whether to drill in via a separate Zabbix MCP / dashboard query — no cross-MCP coupling required.

## [0.3.14] - 2026-05-04

### Changed
- Slack markup is resolved when rendering message bodies: `<@USERID>` becomes `@Display Name` (or `@USERID` when the name is unknown), `<url|label>` collapses to `label`, and bare `<url>` is dropped. Saves ~50–100 tokens per release / MR / mention message.
- `tools.collectUserIDsWithReplies` now also pre-resolves `<@USERID>` users referenced inside message bodies, so the renderer has names available without extra API calls.
- New `format.RenderText(text, users)` and `format.CollectMentionedUserIDs(messages)` helpers; `MessageLine` accepts an optional users map (variadic) to enable in-body mention resolution.

## [0.3.13] - 2026-05-04

### Fixed (LOG MODE — monitoring channels)
- Recognise Zabbix-style `Severity Disaster/High/Average/Warning` labels and map them to FATAL/ERROR/ALERT/WARN bands. Previously every monitoring alert went to INFO.
- `canonicalSignature` strips `Problem:` and `Resolved in <duration>:` prefixes, so the same trigger flapping in/out of state collapses to one pattern with a count instead of N near-duplicates.

## [0.3.12] - 2026-05-04

### Fixed (GIT MODE)
- Slack `<url|label>` markup is stripped before workflow-key extraction. Previously the `branch` regex grabbed `https` from URL markup ahead of the real branch name, causing distinct repos and branches to merge into one nonsense `branch https` line.
- Workflow keys now include the **repo name** (extracted from `of REPO / SUB / NAME` patterns), so the same branch name (`pre-release`) across different repos no longer collapses.
- `Pipeline #N has passed/failed` is recognised as a verb (`pipeline ✓` / `pipeline ✗`).
- Commit subjects (from `<sha>: subject - author` push messages) are now captured and rendered as bullet sublines under each workflow, capped at 3 with `+N more commits` overflow.

## [0.3.11] - 2026-05-04

### Fixed
- `get_mentions(with_context=true)` now also returns messages **after** the mention timestamp, not just preceding ones. Previously, the operator's own subsequent replies were invisible to the digest, causing false "no answer" reports on conversations the operator had clearly responded to. Rendered as `↪` (after) alongside `↳` (before).

## [0.3.10] - 2026-05-04

### Changed
- README "Install in Claude Code" example now defaults to Setup A (user token only — recommended for personal use). Setup B (bot token) is a one-line addendum. Docker section and `docker-compose.yml` follow the same convention.

## [0.3.9] - 2026-05-04

### Changed
- Empty messages (Slackbot pings, webhooks with no body) are now filtered out before rendering. Channels left with no content after filtering are dropped from the digest entirely. Saves ~40% tokens on workspaces with many empty bot pings.
- `formatUserDisplay` is now case-insensitive when deciding whether to suppress the `(handle)` parenthetical: `Slackbot (slackbot)` collapses to `Slackbot`.
- `LogChannelDigest` skips per-band sample listings when every pattern in the band has empty content; the histogram still shows the count.

## [0.3.8] - 2026-05-04

### Added
- Git/CI channels (`#git-*`, `#ci-*`, names containing `deploy`) detected as a stricter sub-class of log channels and rendered in **GIT MODE**: events collated per workflow key (issue ID, MR number, branch name, or deploy target), with the action timeline and actors summarized on one line. Replaces the noisy per-event listing for git-bot feeds.

## [0.3.7] - 2026-05-04

### Added
- `get_mentions` gains `with_context` (bool, default false) and `context_messages` (int, default 3). When enabled, each mention is followed by N preceding messages from the same channel/DM rendered as indented `↳` lines, so short replies like "thanks" / "ok" / "спасибо" carry the prior context inline.

## [0.3.6] - 2026-04-30

### Changed
- `list_users` output now includes `profile_updated=YYYY-MM-DD`. New `with_activity` (bool, default false) opt-in fetches each user's last-message date via `search.messages from:@handle` (parallel, 4 workers) and appends `last_post=YYYY-MM-DD`. Slack does not expose account creation date through the API; profile-update + last-post are the closest seniority signals.

## [0.3.5] - 2026-04-30

### Added
- `list_users` tool — enumerate active workspace users with handle, real name, admin/owner/guest/bot flags. `include_bots` (bool, default false) opt-in for bot/integration accounts. Useful for auditing handle conventions and onboarding gaps.

## [0.3.4] - 2026-04-30

### Changed
- User names in digests render as `"Real Name (handle)"` so the LLM can correlate Slack handles with the human behind them. Falls through to whichever field is available; no parens when the two would be identical.

## [0.3.3] - 2026-04-30

### Fixed
- `Unread()` no longer short-circuits on `unread_count == 0`. Slack reports zero for muted and high-traffic channels even when messages newer than `last_read` exist; the digest now drives off `last_read` alone, surfacing those channels.

## [0.3.2] - 2026-04-30

### Fixed
- **`get_unread_summary` now covers direct messages and group DMs.** Previously, `JoinedChannels` only requested `public_channel` and `private_channel` types from `users.conversations`, silently dropping every DM. Operators saw "all caught up — 0 unread" while `get_mentions` showed dozens of hits in DMs. Types list now includes `im` and `mpim`. See `docs/adr/0009-include-direct-messages-in-unread-sweep.md`.

### Changed
- **Digest headers are now caller-prefixed.** `format.ChannelDigest` and `format.LogChannelDigest` previously hardcoded a `#` prefix in the heading (`## #channelname`). They now take a verbatim `channelLabel` so callers can pick the right prefix per channel kind: `#` for channels, `@peer` for IMs, `mpdm-...` for group DMs. New helper `tools.channelDisplayLabel(ctx, ch, users)` does the routing. LLM consumers that pattern-match on `## #` should relax to `## ` followed by the label.
- README and docker-compose example `SLACK_CHANNELS` switched to generic placeholders.

### Added
- 6 new unit tests in `internal/tools/unread_dm_test.go` covering each branch of `channelDisplayLabel` (regular channel, empty-name channel, mpim with name, mpim without name, im without user, im with user).

## [0.3.1] - 2026-04-30

### Changed
- **Log-mode rendering now dedupes near-identical messages.** A new `canonicalSignature` pass replaces URLs, IPv4 addresses, hex IDs (≥7 chars), and digit runs with placeholders, lowercases, and collapses whitespace. Messages sharing a signature group into a single `LogPattern` rendered as `"[hh:mm bot] body (×N similar)"`. `samplesPerBand` parameter now caps distinct patterns per band (still default 3); same name, sharper semantics. See `docs/adr/0008-log-pattern-dedup.md`.

### Added
- `format.LogPattern{Sample, Count, Signature}` — public type used by `LogBand.Patterns`.
- `LogBand.Patterns []LogPattern` — preferred field; renderer prefers it over the legacy `Samples` path. Existing callers that populate `Samples` keep getting per-message rendering (backwards compatible).
- 33 new unit tests in `internal/tools/dedup_test.go` covering each regex (URL > IP > hex > digit ordering), family-merge invariants, recency tiebreak in pattern sort, top-N + remainder math, and the renderer's pattern + legacy-fallback paths.

### Notes
- Conservative dedup: alerts that differ by alphabetic detail stay distinct (e.g. `"high cpu on dc1"` vs `"high cpu on dc-1"` — hyphen alone won't merge them). This is intentional — over-merging genuinely different incidents costs more than rendering two near-duplicate lines.

## [0.3.0] - 2026-04-30

### Added
- **Log-channel mode in `get_unread_summary`.** Bot-driven channels (monitoring, ci, registry, cloud, etc.) are auto-detected and rendered as a severity histogram (`FATAL=2 ERROR=12 WARN=3 INFO=8`) followed by sample messages per band, instead of the per-message digest used for human conversations. Saves ~70% of the tokens these channels used to consume. See `docs/adr/0007-log-channel-mode.md`.
- **Auto-detection heuristic** — channels are classified as logs when ≥50% of unread messages are bot-authored (`bot_id` set or `bot_message` subtype) OR when the channel name contains `log`, `alert`, `alarm`, `monitor`, `monitoring`, `metric`/`metrics`, `report`/`reports`, `cron`, or `incident`. Name fallback catches webhook-style integrations that post under real user accounts.
- **`log_mode` parameter** (`auto` | `off`, default `auto`) — escape hatch when auto-detection misclassifies.
- **`log_samples_per_band` parameter** (number, default `3`) — cap on the "recent X" sample list per severity band.
- New types: `format.LogBand`, `format.LogChannelDigest`, `tools.LogSeverity` with five bands (FATAL > ERROR > ALERT > WARN > INFO).

### Notes
- Log mode does NOT inline thread replies or mention markers in the rendered output. Bot channels rarely thread, and humans following up on an alert are low-volume; if needed, drop to `log_mode=off` for the full digest.
- Severity classification reuses the same English log-vocabulary as the v0.2.8 urgency keyword block, so a message that bumps urgency for channel ranking will also classify into the matching band.

## [0.2.8] - 2026-04-30

### Added
- **`urgency_weight` parameter on `get_unread_summary`** — multiplier on the urgency score before ranking (default `1.0`). Zero or negative values fall back to the default; pass `0.5` to dampen, `2.0` to amplify. See `docs/adr/0006-urgency-tuning-and-log-channel-keywords.md`.
- **`urgency_keywords` parameter on `get_unread_summary`** — comma-separated extra keywords additive to the built-in en/ru list. Useful for domain-specific terms like `"p0, prod down, internal-tool"` without redeploying.
- **English log-severity keywords in the built-in list** — `error`, `errors`, `failed`, `failure`, `fatal`, `alert`, `exception`, `panic`, `outage`, `timed out`, plus Russian `не отвечает`. Bot-driven channels (zabbix / gitlab / harbor / aws) now surface real failures above routine info without configuration.

### Notes
- Deliberately omitted from the built-in list: `down` (matches `downloaded`/`markdown`/`cooldown` — too noisy) and `fail` (superset of `failed` and `failure`, would double-score). Both can still be added via `urgency_keywords` on a per-call basis.

## [0.2.7] - 2026-04-30

### Changed
- **`get_unread_summary` now ranks channels by urgency, not just volume.** A new heuristic in `internal/tools/urgency.go` scores each unread channel from per-message signals: question marks (capped at 3 per message), urgency keywords in English and Russian (`urgent`/`срочно`/`сломалось`/...), urgency-suggesting reactions (`rotating_light`, `fire`, `warning`, ...), and recency (`<1h` and `<6h` bands). A single keyword outranks ~9 plain messages; mentions of the operator still dominate any non-mention channel. See `docs/adr/0005-urgency-heuristic-for-unread-ranking.md`.

### Added
- `internal/tools/urgency.go` + `internal/tools/urgency_test.go` (14 cases): per-signal tests, recency bands, ranking-interaction invariants (mention > urgency > volume), full-width `？` handling, keyword case-insensitivity in Cyrillic.

## [0.2.6] - 2026-04-30

### Added
- **`mentions_only` parameter on `get_unread_summary`** — when true, returns only channels containing at least one direct `<@U_OPERATOR>` mention (top-level or in a thread reply). Header switches to `# Unread summary (mentions only)` so callers can distinguish. See `docs/adr/0004-unread-summary-mentions-only-and-reply-cap.md`.
- **`thread_preview_replies` parameter on `get_unread_summary`** — overrides the per-thread inline reply cap (default 3). Plumbed through as `format.WithThreadPreviewReplies(n)`; non-positive values fall back to `format.ThreadPreviewReplies`.

### Changed
- Tool helpers in `internal/tools/unread.go` (`channelMentions`, `filterMentions`, `rankUnread`, `collectUserIDsWithReplies`) are now covered by `internal/tools/unread_helpers_test.go` — 16 new cases.

## [0.2.5] - 2026-04-30

### Added
- **Thread context in `get_unread_summary`.** Top-level unread messages that are thread parents now have their post-`last_read` replies fetched and rendered indented (`↳ ...`) under the parent. Capped at 3 replies per thread with a `+N more replies` collapse for the rest. See `docs/adr/0003-unread-summary-thread-context-and-mention-marker.md`.
- **Mention markers (`🏷️`) in the unread digest.** Messages whose body contains `<@U_OPERATOR>` are prefixed with a marker character so the LLM (and the human) can spot direct asks at a glance. The operator's user ID is resolved once via `auth.test` and cached.
- **Mention-aware channel ranking.** `get_unread_summary` now sorts channels with at least one direct mention ahead of busier-but-impersonal channels.
- `UnreadService.Self(ctx)` — cached self-user resolution for tools that need to know who "you" are (Slack-side).
- `format.WithMentionHighlight` / `format.WithThreadReplies` — variadic `DigestOption` API; existing `ChannelDigest` callers unchanged.

### Limits
- Replies on threads whose parent is *already* read are not surfaced by `get_unread_summary` (would require a per-channel `latest_reply > last_read` scan). Use `get_mentions` for that case — it hits `search.messages` and catches the mention regardless of thread state.

## [0.2.4] - 2026-04-30

### Fixed
- **stdio transport now exits when its parent MCP host process dies.** Previously, hosts that disconnected without closing stdin (e.g. some Claude Code reconnect paths) left orphan `slk-mcp` processes around, requiring manual `pkill`. The new `internal/lifecycle.WatchParent` polls `os.Getppid()` and exits when it changes (parent reparented to PID 1 / launchd). See `docs/adr/0002-parent-pid-watcher-for-stdio-orphans.md`.

### Added
- `internal/lifecycle` package with `WatchParent` plus 6 unit tests covering ppid-change detection, single-shot semantics, context-cancel exit, nil-logger safety, and zero-interval default behaviour.

## [0.2.3] - 2026-04-30

### Fixed
- **`get_unread_summary` no longer reports "0 unread" when channels have new messages.** `UnreadAll` was filtering channels using `unread_count` from `users.conversations`, which Slack does not populate on that endpoint. Filter is now driven by the per-channel `conversations.info` lookup (which already short-circuits when caught up). See `docs/adr/0001-unread-summary-trusts-conversations-info.md`.

### Added
- Unit tests for the unread service (`internal/slack/unread_test.go`) covering the regression, token gating, last-read boundary handling, pagination, and `mark_read`.

## [0.2.2] - 2026-04-30

### Added
- **Channel auto-discovery** — when no `channels` argument is passed and `SLACK_CHANNELS` is empty, the digest, recap, and decision tools fall back to every channel the active identity has joined (user token: `users.conversations`; bot token: bot's joined channels).
- New env var `SLACK_AUTODISCOVER_LIMIT` (default `50`) caps the auto-discovered list.
- `Client.JoinedChannelNames(ctx, limit)` — single entry point for the active identity's channels, sorted by member count, archived filtered.

### Changed
- `parseChannelList` no longer takes a defaults argument; channel resolution is now centralised in `resolveTargetChannels` (input → config → auto).
- Tools log the resolved channel count when auto-discovery runs.

## [0.2.1] - 2026-04-14

### Changed
- **Token model is now flexible** — at least one of `SLACK_TOKEN` (`xoxb-`) or `SLACK_USER_TOKEN` (`xoxp-`) is required, not both. A user-only setup is now fully supported and acts as the authenticated user for all API calls (posts appear under the user's name).
- `slack.Client` picks the primary API from `Config.PrimaryToken()`; when the user token is primary, the bot HTTP client pool is not allocated.
- Startup log now reports the active token mode (`bot-only`, `user-only`, `bot + user`).
- `ErrMissingBotToken` → `ErrMissingToken`.
- README: split "Create a Slack App" into user-only (Setup A) and bot (Setup B) recipes.

## [0.2.0] - 2026-04-14

### Added
- **`get_unread_summary`** — smart summary of every unread message across joined channels, grouped per channel. Requires `SLACK_USER_TOKEN`.
- **`get_mentions`** — direct mentions of the authenticated user over a time window.
- **`mark_read`** — mark a channel as read up to a given message timestamp.
- **User token support** (`SLACK_USER_TOKEN`, `xoxp-...`) alongside the bot token. Gates unread/mentions/mark_read tools.
- **Rate-limit handling** — `internal/slack/ratelimit` retries on `429` with the `Retry-After` value Slack returns, up to 5 attempts.
- **Context propagation** — every Slack API call uses the `*Context` variant, so tool cancellation and timeouts flow through.
- **Compact output formatter** (`SLACK_COMPACT=true` by default) — single-line messages with truncation markers (`+127 chars`) and per-channel caps (`+N more messages`) to reduce LLM token consumption.
- **Structured logging** with `log/slog` (JSON, stderr). Configurable via `-log-level` or `SLACK_LOG_LEVEL`.
- **Graceful shutdown** on SIGINT/SIGTERM with a 10 s timeout for HTTP transports.
- `-version` flag.
- Unit tests for config parsing and formatters.

### Changed
- **Architecture** — `slack.Client` now composes narrow services: `Channels`, `Messages`, `Users`, `Search`, `Unread`. Tool handlers depend on services they actually use.
- **User name resolution** — cached behind a `sync.RWMutex` and batched via `UserService.NamesFor`.
- **Search** now prefers the user token when configured (`search.messages` is gated on user tokens for newer Slack apps).
- Tool handlers now consistently return `NewToolResultError` for user-facing errors and use `errors.Is` / wrapped errors internally.
- `DISABLED_TOOLS` is checked per-tool at registration time instead of post-hoc in the `_tool_manager` map.

### Fixed
- Concurrent access to the channel/user caches is now safe.
- Whitespace collapsing in message bodies (multi-line messages no longer break single-line output).

## [0.1.0] - 2026-04-14

### Added
- Initial release
- 10 tools: list_channels, get_channel_info, get_channel_digest, get_multi_channel_digest, get_morning_recap, search_messages, find_decisions, get_thread, get_user_messages, post_message, add_reaction
- Morning recap with decision detection (keywords + reactions)
- Multi-channel digest in a single call
- Slack search syntax support (from:@user, in:#channel, has:link)
- Read-only mode (`SLACK_READ_ONLY=true`)
- Tool filtering via `DISABLED_TOOLS`
- Default channels via `SLACK_CHANNELS` env var
- Docker support (Alpine-based, ~15 MB image)
- SSE and streamable-http transports

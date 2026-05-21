# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

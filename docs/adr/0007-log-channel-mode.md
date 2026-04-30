# ADR 0007: log-channel mode in `get_unread_summary`

- Status: Accepted
- Date: 2026-04-30
- Builds on: ADR 0005 (urgency heuristic), ADR 0006 (urgency tuning).

## Context

A typical operator workspace has a long tail of bot-driven channels —
zabbix triggers, gitlab pipeline events, harbor scans, aws cloudwatch
alarms, daily-cron reports. They produce dozens to hundreds of
near-identical messages per day. Rendering them with the regular
`ChannelDigest` (one line per message, capped) wastes tokens on
99%-redundant content while losing the only thing operators want from
those channels: the severity histogram and a few representative
samples per band.

Until v0.2.8, log channels had to share a rendering format with
human-conversation channels. Operators worked around it by reading
log channels manually in the Slack web UI.

## Decision

Add a log-channel rendering mode. New surface:

- **`internal/tools/logchannel.go`**:
  - `classifyLogSeverity(msg) → LogSeverity` — text-based band
    detection. Strongest match wins (`fatal`/`panic` → FATAL,
    `error`/`failed`/`failure`/`exception` → ERROR,
    `alert`/`outage`/`timed out`/`не отвечает` → ALERT,
    `warn`/`warning` → WARN, otherwise INFO).
  - `detectLogChannel(cu) → bool` — heuristic, two signals OR'd:
    - **Bot authorship** — ≥50% of unread messages have `bot_id`
      set or subtype `bot_message`.
    - **Channel name pattern** — fallback for webhook integrations
      that post under a real user account. Substrings: `log`,
      `alert`, `alarm`, `monitor`, `monitoring`, `metric`,
      `metrics`, `report`, `reports`, `cron`, `incident`.
  - `buildLogBands(messages, samplesPerBand)` — group by
    severity, sample top-N most-recent per band, return in
    dominance order (FATAL → INFO).

- **`internal/format/format.go`**:
  - `LogBand{Label, Samples, Total}` — rendering input.
  - `LogChannelDigest(channelName, total, bands, users)` —
    severity histogram (`severity: FATAL=2 ERROR=12 WARN=3 INFO=8`)
    followed by per-band samples with truncation markers
    (`... +N more`).

- **`get_unread_summary`** — auto-routes per channel by default.
  New parameters:
  - `log_mode` (string, `"auto"` | `"off"`, default `"auto"`) —
    escape hatch when the heuristic misclassifies a channel.
  - `log_samples_per_band` (number, default `3`) — cap on the
    "recent X" listings per severity band.

## Why these specific heuristics

- **Bot threshold at 50%**: most genuine bot channels are 90%+
  bot-authored; lowering to 30% would catch hybrid channels
  (e.g. `#team-alpha` where humans announce and a bot confirms),
  but those benefit from regular-digest output. 50% lets the
  human-chat half of mixed channels stay readable.
- **Name substrings only, no regex**: the keyword list comes
  from common bot-channel naming conventions — bare prefix/suffix matching
  is enough and cheaper to test. The 8-keyword list covers all
  the standard infra-channel naming conventions encountered.
- **Severity classification on body text only**: zabbix and gitlab
  do not add structured severity to Slack messages. Re-using the
  same English log-severity vocabulary as the v0.2.8 urgency
  keyword block keeps the two systems consistent — a message
  that bumps urgency for ranking will also classify into the
  matching band for log-mode rendering.

## What we deliberately did NOT do

- **Pattern dedup** ("×6 trigger fired: high cpu load") — would
  need a stable signature scheme that strips IDs/numbers/URLs.
  Failure mode (over-merging distinct-but-similar alerts) is
  worse than the current verbose listing. Revisit when there is
  user demand and concrete examples of safe canonicalisation.
- **Replies / threads in log mode** — bots rarely thread, and
  human follow-ups on an alert are normally low-volume. Log mode
  ignores `ChannelUnread.Replies`; operators wanting follow-up
  context can drop into the regular digest with `log_mode=off`.
- **Mention markers in log mode** — log channels usually don't
  `<@U_OPERATOR>` you (cron pings rarely tag specific people). If
  one does, it'll surface via `mentions_only=true` since
  mention-detection runs before the format split.

## Consequences

- A `get_unread_summary` call against a workspace with 20 alert
  channels and 30 human channels now produces a digest where the
  log channels render in 5–10 lines each (histogram + samples)
  instead of 20+ per-message lines, saving roughly 70% of the
  tokens those channels used to consume.
- Test coverage: `internal/tools/logchannel_test.go` — 53 cases
  covering severity classification (positive + negative),
  bot-message detection variants, the channel-name keyword list
  against real workspace names, the bot-percentage threshold at
  edges (3-of-4 vs 1-of-4), the empty-channel guard, the
  build-bands ordering and sample cap, and the digest renderer's
  histogram + sample + zero-band-skip behaviour.
- Tools layer is now ~570 LOC across `unread.go`, `urgency.go`,
  and `logchannel.go`. Time to keep an eye on whether splitting
  the unread tool into its own subpackage becomes worthwhile.
  Not yet — but flagged.

# ADR 145: dependencies to latest – slack-go 0.35, mcp-go 1.5, Go 1.26

Date: 2026-11-11
Status: accepted

## Context

The two direct dependencies had fallen far behind: slack-go 0.15
(latest 0.35) and mcp-go 0.32 (latest 1.5). GitHub reported two open
advisories on `main`, one in slack-go and one in an indirect WebSocket
library. Several of today's fixes worked around slack-go limits that
newer releases address. A large gap only grows: the longer it waits,
the more breaking changes land in one upgrade.

## Decision

Upgrade everything to the latest release in one step, and port the code
without changing any tool's behaviour.

- **Modules:** every direct and indirect module to its latest version.
  The `go` directive rises from 1.23 to 1.26 because the new mcp-go
  requires it; the Docker build image and the README badge follow.
- **slack-go 0.35 changes that touched us:**
  - `GetReactionsContext` returns the reacted item, not the bare list;
    `Reactions` reads its `Reactions` field. Same result for callers.
  - File downloads send the token only to an https Slack host or to the
    configured API host. Real files are on `files.slack.com`, so nothing
    changes in use; a remote file whose URL points elsewhere is no longer
    sent our token, which is the point of the change. Tests now serve
    downloads from a Slack host (via the fake transport) or from the API
    host.
- **mcp-go 1.5:** a tool call's result arrives by pointer. The headless
  runner already accepted both shapes; the two test harnesses now do too.
- **CI:** actions to their latest majors (checkout 7, setup-go 7,
  golangci-lint-action 9); the Go matrix moves to 1.26 and 1.27; lint
  runs on 1.27. golangci-lint 9 brings golangci-lint 2, whose config
  format differs; `.golangci.yml` is migrated, keeping the v1 scope:
  staticcheck limited to the SA (correctness) checks and v1's default
  exclusions restored through presets.

## Consequences

- Building from source needs Go 1.26 or newer.
- The required status checks on `main` are named after the Go versions
  ("Test (Go 1.26)", "Test (Go 1.27)") and must be updated with this
  change, or merges wait for checks that no longer run.
- Major upgrades of these two libraries are reviewed one at a time,
  against their changelogs; patch and minor updates can follow
  Dependabot.

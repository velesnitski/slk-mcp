# ADR 088: releases and dependency updates stop depending on memory

Date: 2026-09-02
Status: accepted

## Context

A third-party MCP scanner scored this repository 0/100 while marking two
of its four categories as passing. The number is not usable — two passes
cannot produce a zero — but two of its marks pointed at real gaps, and
those are worth closing regardless of who was asking.

**Releases had drifted seven tags behind.** The repo was at `v1.40.0`
and the Releases page showed `v1.36.0`, published a week earlier. The
work was continuous; the page said otherwise. Releases were created by
hand, so they were created when someone remembered.

**Dependencies had no automation at all.** No `dependabot.yml`, so
neither Go modules nor the pinned GitHub Actions were watched. Two direct
dependencies is a small surface, but an unwatched small surface is still
unwatched, and the Actions pins age fastest.

## Decision

**One source for release notes.** `scripts/changelog-section.sh` prints
a version's section from `CHANGELOG.md`. The release workflow and anyone
publishing by hand both read it, so the notes on GitHub cannot drift
from the notes in the repo — there is nowhere for a second version to
live.

**Publishing runs on the tag.** `.github/workflows/release.yml` fires on
`v*` and creates the release from that section. Tagging is the decision;
publishing is bookkeeping, and bookkeeping that depends on memory
eventually stops happening. Two properties matter:

- A tag with no CHANGELOG entry **fails the workflow** rather than
  publishing empty notes. An untagged changelog entry is an authoring
  mistake and should be loud.
- Re-running on an existing release **updates** it instead of failing,
  so a re-run is always safe.

**Dependabot targets `dev`, not `main`.** Everything reaches main by
fast-forward from dev; a bot opening PRs at main would be the one path
that skips that, and main is protected. Minor and patch updates are
grouped into a single weekly PR — six separate PRs against a
two-dependency project is how dependency automation gets muted, and a
muted bot is worse than none because it looks like coverage. Majors stay
ungrouped: they need reading, not batching.

**Backfilled to v1.32.0, not to v1.0.0.** Eleven releases across two
months is what an active project looks like. Recreating fifty releases
from the project's first months would be archaeology, and doing it in one
burst would misrepresent the activity feed.

## Consequences

- The Releases page now matches the tags and keeps matching without
  anyone maintaining it.
- Dependency and Actions updates arrive weekly as one reviewable PR that
  runs the full CI matrix before it can merge.
- Tags older than `v1.32.0` stay without releases. Deliberate.

## Not done here: commit signing

The remaining verification gap is that commits and tags are unsigned.
Closing it needs a signing key, and generating key material is the
maintainer's action, not something to automate from a session. The local
git configuration is a two-command change once a key exists; the key
must also be registered with GitHub as a *signing* key, which is a
separate setting from the authentication key.

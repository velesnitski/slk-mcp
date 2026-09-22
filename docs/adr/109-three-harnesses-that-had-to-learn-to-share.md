# ADR 109: three harnesses that had to learn to share

Date: 2026-09-22
Status: accepted

## Context

ADR 107 added `config.APIURL` so handler tests could drive the real
stack against an `httptest` server. The coverage work that followed was
split across six parallel workers, each owning a disjoint set of files,
each in its own worktree cut from the same commit.

Most of the worktrees were cut *before* the ADR 107 commit landed. So
four of the six could not use the seam it added, and each independently
solved the same problem a layer lower: replace `http.DefaultTransport`
with a round-tripper that answers `slack.com` from an in-memory route
table. Three of those ended up in `internal/tools` together.

Every one of them passed in isolation. Merged, the package failed.

Go runs `init()` in file-name order within a package, so the three
installed in sequence — `de_`, then `ua_` — each saving the transport it
replaced. That part was fine: two of them delegated to the saved
transport for any request they did not recognise, which composes. The
third answered *every* `slack.com` request from its own table and
returned an error when no fixture was registered, on the assumption that
an unrouted request meant a test had forgotten a route. Once it was
installed last, it was the only harness that worked, and 24 tests
belonging to the other two failed with connection errors.

The fix is one line — delegate instead of erroring — but the failure is
worth recording because nothing local to any worktree could have caught
it. Each harness was correct alone, and the defect existed only in the
combination.

## Decision

A test transport in this package claims a request only when it has a
fixture for it, and delegates to the transport it replaced otherwise.
"I am installed" is not a claim of ownership; "I was configured for
this request" is. Installation happens once from `init()` and the global
is never written again, so the chain is fixed before any test runs and
no test mutates it.

`config.APIURL` (ADR 107) remains the preferred seam for new tests: it
needs no global, composes with everything by construction, and scopes to
one client rather than the process. The transport harnesses stay because
they work, are covered, and rewriting three of them would buy nothing —
but they are the older way, not the pattern to copy.

The "unrouted method" signal that motivated the erroring behaviour is
still worth having. It belongs in the fake's own response
(`{"ok":false,"error":"not_mocked"}` for a request the fake *does* own),
not in the transport's decision about whose request it is.

## Consequences

- Coverage went 56.1% to 93.5% overall; `internal/tools` 48.9% to 95.6%,
  `internal/slack` 55.2% to 91.6%, and `internal/slack/ratelimit` from
  no test file at all to 100%. 1371 tests pass under `-race
  -shuffle=on`.
- Three harnesses coexist in one package. That is redundancy, and the
  cost is that a newcomer must pick one; the doc comments say which.
- Parallel test authoring against a shared process global needs the
  composition rule stated up front. It was not, and the merge paid for
  it. Next time the rule ships with the assignment.
- The suite now covers the handler bodies where the v1.46–v1.50
  silent-failure defects lived. Writing it surfaced eleven more of the
  same family — recorded in the commit that fixes them, not here.

# ADR 107: the API base URL is a test seam, not a setting

Date: 2026-09-22
Status: accepted

## Context

Coverage of `internal/tools` sat at 48.9% while the package holds the
bulk of the code. The reason was structural, not neglect.

Handlers reach Slack through `Hub.Users()`, `Hub.Messages()` and the
other accessors. Those return the narrow interfaces from
`contracts.go`, which reads like an injection seam and was introduced
as one — but the method bodies return `h.client.X`, the concrete
service. Go has no virtual dispatch, so a test cannot embed `Hub` and
override an accessor: `buildMentions` calls `h.Users()` on its own
`*Hub` receiver and gets the real service back. The interfaces document
the dependency; they do not sever it.

That left every handler needing a live Slack to run, so the tests that
existed covered pure helpers and left the handler bodies — argument
parsing, the call itself, error branches, rendering — untested. The
five silent-failure defects fixed in the v1.46–v1.50 series all lived
in exactly that untested band, and all had the same shape: a tool that
returned less than it should while reporting success.

Two options were on the table.

**Hand-built fakes per interface.** No production change. But a fake
satisfying `UserClient` only proves the handler called a method with
some arguments. It cannot catch a request slack-go would serialise in a
form Slack rejects, which is the class of bug that has actually cost us
releases. It also means writing and maintaining ten fakes whose drift
from the real services is invisible until production.

**Redirect the Web API.** slack-go accepts `OptionAPIURL`, so pointing
the client at an `httptest` server exercises the whole path — handler,
service layer, slack-go's own encoding, HTTP — against responses we
control. One seam, and every handler becomes testable.

The objection to the second option is real: a base-URL override sends
bearer tokens wherever it points. Bound to an environment variable, it
would be a way to exfiltrate a workspace token from a deployed server
by editing its environment — a capability this server has no reason to
grant.

## Decision

`config.Config.APIURL` redirects the Slack Web API, and nothing reads
it from the environment. `Load` never sets it; there is no
`SLACK_API_URL`. The only way to populate it is to construct a `Config`
in the same process, which a test does and a deployed server cannot.
The security property is not "the variable is undocumented" — it is
that no code path from the environment to the field exists.

`slack.New` passes `OptionAPIURL` to both goslack clients when the
field is set. `Lists` and `Canvas` call Slack over raw HTTP rather than
through slack-go, so `OptionAPIURL` does not reach them; their
pre-existing `BaseURL` overrides, added for the same reason before this
ADR, are pointed at the same base.

`internal/tools/slackfake_test.go` provides the harness: a programmable
`httptest` server routing Slack methods to canned responses, and
`newFakeHub` to build a `Hub` against it. Unrouted methods answer
`{"ok":false,"error":"not_mocked"}` and are recorded, so a handler that
reaches for an unanticipated endpoint fails as a readable assertion
rather than a hang or a live request.

The `contracts.go` interfaces stay. They still enforce the narrow
surface at compile time and remain the right tool for a unit test of
one branch. They are simply no longer the only way to test a handler.

## Consequences

- Handler tests exercise the request slack-go actually sends, so a
  serialisation change in slack-go or a parameter our service builds
  wrongly fails a test instead of shipping.
- A deployed server cannot be redirected by its environment, which was
  not true of the rejected env-var design and is the reason the field
  is plain rather than convenient.
- The harness is the intended default for new handler tests. Interface
  fakes remain available and are still the cheaper choice for a pure
  branch that never touches the wire.
- `Config` now carries a field that exists for tests. That is a real
  cost and the comment on it says so; the alternative was ten fakes
  that could drift from the services they stand in for without anything
  failing.

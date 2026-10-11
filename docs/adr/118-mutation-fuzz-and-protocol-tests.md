# ADR 118: mutation, fuzz and protocol-level tests

Date: 2026-10-11
Status: accepted

## Context

Line coverage was already above 90%, yet recent releases shipped bugs
that the suite passed: fakes that did not page like Slack (ADR 113) and
a PDF reader that silently lost picture pages (ADR 117). Coverage says a
line ran, not that a test would notice it changing. Three gaps:

- No measure of assertion strength.
- Every parser of untrusted input — PDF, HTML, XLSX, Block Kit,
  permalinks — was tested only on hand-written examples, though any
  member of a workspace can post the files and links they read.
- No test spoke the protocol. Handlers were called directly; tool
  registration, JSON-RPC and the transport were never exercised
  together.

## Decision

- **Mutation testing** with gremlins, default and extended mutators
  (logical inversion, loop control, assignments, bitwise), across all
  internal packages: 3,136 mutants, none survived. `make mutate` reruns
  it and fails below 100% efficacy. Uncovered mutants were reviewed;
  those in the PDF reader marked real encodings with no fixture
  (string escapes, hex strings, line operators, contents arrays,
  resource references) and now have tests.
- **Fuzz targets** for the PDF reader and page walk, HTML conversion,
  document conversion, XLSX, the closer filter, permalink and file-URL
  parsing, message text rendering and Block Kit rendering. Each states
  an invariant beyond "no panic" where one exists. CI runs the seed
  corpus; `make fuzz FUZZTIME=…` explores.
- **Protocol integration tests**: `buildServer` (extracted from `run`)
  is served over the streamable-HTTP transport, an mcp-go client
  initializes, lists and calls tools, and Slack is a fake over HTTP.
  They check the server identity, read-only hiding of write tools, a
  digest reaching Slack and rendering, and bad arguments surfacing as a
  tool error rather than a protocol failure.

## Consequences

- Writing the page-walk tests exposed a defect: an uncompressed picture
  page with no text operator was dropped by the generic stream decoder
  and never flagged. Content streams named by the page tree are now
  read regardless.
- Mutation runs take about fifteen minutes and stay out of CI; fuzzing
  beyond the seeds is likewise manual.

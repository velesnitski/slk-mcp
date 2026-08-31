# ADR 082: credential fixtures are assembled, and the tree has no escape hatch

Date: 2026-08-31
Status: accepted

Supersedes the working-tree half of the `sweep:allow` decision in ADR 079.

## Context

The redaction suite must contain credential-shaped strings, or it proves
nothing: `Redact` is only tested by feeding it something that matches its
own patterns. Those fixtures were written as literals — an AWS key ID
shape, a Slack bot token shape, GitHub and GitLab PAT shapes, a PEM block
— each carrying a `sweep:allow` comment so the sweep would let them past.

None of them could ever authenticate. The bodies are filler. But:

- **A secret scanner matches on shape, not on validity.** This repository
  is public, so those literals produce hits that somebody has to triage,
  and produce them in public.
- **The marker was the only escape hatch in the guard, and it trained the
  wrong reflex.** Writing `// sweep:allow` is easier than restructuring a
  fixture, so the exemption grows. It was also subtly scoped — shapes
  only, deny-list still applied — which is a distinction a future reader
  will not reliably hold.
- The project rule is already stricter than the guard was: no key or
  token material anywhere, test fixtures included, fake shapes included.

## Decision

**Assemble every credential-shaped fixture at runtime.** Each shape is
built by a named helper whose comment writes out the shape in prose, so a
reader loses nothing, while the source file contains no contiguous span a
scanner reads as a credential. All six marker uses are gone.

**The working tree honours no exemption at all.** With zero users, the
tree-scan exemption is free to remove, and removing it means the reflex
has nowhere to go: a credential shape in a tracked file always fails.

**History keeps the exemption.** The old blobs carry those literals — 12
shape-hits in the object store — and they are immutable. Rewriting
history to scrub synthetic fixtures would cost a force-push across every
clone to remove strings that were never secret. Deny-list patterns still
apply to marked lines even in history, so the marker cannot hide a real
identifier there either.

One test guards the indirection itself: a helper that quietly stopped
producing a credential shape would leave every redaction test passing
against ordinary prose. `TestFixturesMatchTheShapesUnderTest` asserts
each fixture still matches exactly one pattern.

## Consequences

- Scanner hits on the working tree drop to zero; the ones in history
  remain and are explainable in one sentence.
- This is hygiene, not protection. The strings exist at runtime either
  way. What changes is that the repository stops carrying them and the
  guard stops offering a way to add more.
- A future fixture that genuinely needs a credential shape must be
  assembled. There is no longer a documented way to do otherwise, which
  is the intent.
- Verified by negative test: a `sweep:allow`-marked shape added to a
  tracked file now exits 1.

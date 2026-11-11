# ADR 137: post_message turns typed @names into real mentions

Date: 2026-11-11
Status: accepted

## Context

`chat.postMessage` sends text as is. A message written with
`@some.handle` or `@First Last` arrives as plain text: nobody is
notified, and the author finds out only when half the people never
answer. Slack's mention token is `<@U…>`, an ID that a person writing
the message does not know. Slack's own `link_names` flag resolves
handles only, while people know each other by the names the client
shows: the display name or the real name, often with a space in it.

## Decision

`post_message` resolves mentions against the target workspace's roster
before posting. A new `resolve_mentions` argument, on by default, turns
it off.

- **Three names per person.** After `@`, a handle, display name or real
  name matches, case-insensitive, Cyrillic included. The longest name
  that fits wins, so `@First Last` beats a user called `First`.
- **Word boundaries.** A name must end where a word ends: `@sam` does
  not match inside `@samuel`, and `@sam.` at the end of a sentence
  matches `sam`, while `@sam.lee` needs a person called `sam.lee`.
- **Never guess.** A name two people share is left as typed and
  reported with both candidates; the full name picks one.
- **Left alone:** `@` inside a word (e-mail addresses), text in
  `` `code` `` and code blocks, anything already inside `<…>` (existing
  mentions and links), and `@here`, `@channel` and `@everyone`, which
  notify a whole channel and must not appear by accident.
- **The result says what happened:** who was tagged, and which @words
  stayed text because nobody or more than one person has that name.
- **Fail open.** If the roster cannot be read the message goes out as
  typed, and the result says mentions were not resolved. The dedup guard
  compares the resolved text, which is what Slack stores.

## Consequences

- A message with an `@` costs one `users.list` read before the post.
  Messages without `@` cost nothing extra.
- Pasted lists of handles or names notify everyone on them.
- An `@word` meant as plain text that happens to match someone now
  notifies that person; `resolve_mentions=false` keeps the old
  behaviour.

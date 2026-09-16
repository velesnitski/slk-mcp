# ADR 101: one permalink, one workspace

Date: 2026-09-16
Status: accepted

## Context

`routeWorkspace` exists so a pasted permalink lands in the workspace it
came from: it matches the link's host against each workspace's own team
URL, and says so in the result when more than one is configured.

`get_message` used it. `get_thread` did not — it called
`scopedWorkspace`, which honours an explicit `workspace` argument and
otherwise returns the primary.

So the two tools disagreed about the same link. A permalink copied out
of the second workspace resolved against the primary, the channel was
not there, and the tool answered `channel_not_found`. That error names
the channel, so it reads as "this thread does not exist" when the truth
is "you were pointed at the wrong workspace". The caller then goes
looking for a deleted message.

The same handler also printed its header as `"thread #" + channel`,
which is the defect ADR 099 closed for digest headings and which had
survived here: a DM came back as `#@person`, a channel name that
already carried its sigil as `##name`.

## Decision

`get_thread` routes through `routeWorkspace`, exactly as `get_message`
does, and surfaces the same note when the workspace was inferred rather
than given.

The header uses `conversationLabel`, so a channel gets one `#`, a DM
keeps its `@`, and a bare conversation id is left undecorated.

## Consequences

- A permalink means the same thing to every tool that accepts one.
- A cross-workspace thread read stops reporting a wrong-place error as
  a missing thread.
- Callers see which workspace answered, instead of assuming the primary.

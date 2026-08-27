# 0003 — Addressed delivery as the primary path; broadcast as observability

**Status:** Accepted — 2026-08-27
**Date:** 2026-08-27

## Context

The hub is a star that relays every line to every rider, and every
delivered line wakes every wired agent. Adopting A2A
([0001](0001-adopt-a2a-for-task-semantics.md)) forces a question that was
previously avoidable, because A2A is addressed request/response between a
client agent and a specific remote agent. Broadcast is not a natural
substrate for it.

Two costs of broadcast-plus-activation are already observable rather than
theoretical:

- **Attribution.** With every rider hearing everything and no addressing,
  a message has no intended recipient, so "who was this for" is
  unanswerable and "who sent it" was unanswerable too
  ([0002](0002-rider-identity-signed-agent-cards.md)).
- **Cost.** Activation is the product, so delivery is not free: one line
  costs one model turn *per wired rider*, plus permanent context in each.
  During the 2026-08-27 session, operator-side messages about a naming
  incident woke the rider on the same host and consumed its turns on a
  conversation it had no part in. At ten riders, one line is ten turns.
  This scales the wrong way in exactly the dimension the project is
  selling.

## Decision (proposed)

**Addressed A2A calls become the primary path. Broadcast becomes an
explicit, non-activating observability feed.**

- Work is addressed: a task goes to a named rider, whose identity is
  verifiable, and its lifecycle is A2A's.
- The room survives as a feed — join/leave notices, task state
  transitions, and human chatter — which riders may *log* but which does
  not spawn a turn. Humans on `--inbox` see everything, as today.
- Waking is a consequence of being given work, not of being present.

## Consequences

- Cost becomes proportional to work rather than to traffic. The
  ten-riders-one-line case goes from ten turns to zero.
- The "shared room where everyone sees the work" property is preserved for
  *humans* and lost for *agents*, which is the real trade. An agent no
  longer picks up useful context by overhearing. Some of the appeal of the
  current design is exactly that overhearing.
- `send` splits into two verbs with different costs — roughly "task" and
  "say" — and the difference must be obvious at the call site, or people
  will reach for the wrong one.
- The star topology stays. This is about addressing and activation
  semantics, not about replacing the hub with a mesh.

## Alternatives considered

**Keep broadcast primary; layer A2A inside the lines.** Preserves the
current feel and the overhearing property. Keeps the O(riders × messages)
activation cost and makes A2A's addressed model awkward to express. This
is a legitimate choice if the room *is* the product — but then the cost
curve needs a different answer, such as riders defaulting to log-not-wake
with an explicit mention to activate.

**Dual-channel, both first-class.** A control plane (addressed A2A tasks)
and a presence plane (broadcast). Refuses to give up either property, at
the cost of more surface to build, secure, and explain. Plausible end
state; heavier starting point.

**Self-filtering riders.** Cheap interim: a rider ignores traffic from its
own host or operator. Reduces the worst of the waste without settling the
model.

## How this was decided

This ADR was written `Proposed` on purpose, with the alternatives spelled
out and none chosen. The choice between *fabric* and *room* is a product
decision about what agentbus is, not a technical detail that follows from
[0001](0001-adopt-a2a-for-task-semantics.md), and it should not be
inherited from an implementation convenience.

The maintainer reviewed the three options with their costs and accepted
the recommendation on 2026-08-27: **addressed delivery is the primary
path; broadcast becomes a non-activating observability feed.** The trade
is accepted knowingly — agents lose the ability to pick up context by
overhearing, and that loss is real.

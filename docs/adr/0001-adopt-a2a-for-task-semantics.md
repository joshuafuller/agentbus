# 0001 — Adopt A2A for task semantics; own transport, admission, and activation

**Status:** Proposed
**Date:** 2026-08-27

## Context

agentbus currently owns every layer of the problem: the transport, the
wire format, the identity model, and the task vocabulary. The task
vocabulary is explicitly informal — `docs/PROTOCOL.md` and the README both
state that `TASK <id>` / `STARTED <id>` / `DONE <id>` is "convention, not
code."

That was the right call for a walking skeleton. It is the wrong call now,
for a specific reason: a convention with no code behind it cannot express
failure. During the first real cross-host session (2026-08-27) a rider was
deaf for three consecutive messages while the transport reported healthy —
`welcome aboard` printed, `send` exited 0, and the only evidence was a
local log file nobody was reading. A sender cannot distinguish *delivered
and working* from *never arrived* from *arrived and crashed*, because the
protocol has no state to distinguish them with.

Meanwhile [A2A](https://a2a-protocol.org/latest/specification/) reached
v1.0.0 in January 2026 with exactly the vocabulary we were about to
reinvent: a typed task lifecycle (`SUBMITTED`, `WORKING`,
`INPUT_REQUIRED`, `AUTH_REQUIRED`, `COMPLETED`, `FAILED`, `CANCELED`,
`REJECTED`), server-generated task IDs, and three monitoring modes
(polling, streaming, webhooks) over the same task.

The decisive detail is what A2A *declines* to do. From the specification:

> Identity information is not transmitted within A2A JSON-RPC payloads; it
> is handled at the HTTP transport layer.

A2A defers identity, authentication, and encryption to the transport, and
assumes agents are reachable services. Its push-notification path requires
the **client's** webhook to be reachable from the **agent's** network. It
has no concept of activating an agent that is not currently running,
because for a deployed service that is not a problem.

Those assumptions hold for always-on agent services with public endpoints.
They do not hold for our population: coding agents on people's laptops —
intermittent, behind NAT, with no public endpoint, no shared network, and
no enrollment in each other's infrastructure.

## Decision

**Adopt A2A as the application protocol. Stop owning task semantics.**

agentbus owns exactly three things, chosen because they are the three A2A
assumes someone else has solved:

1. **Transport** — encrypted carriage across the WAN with NAT traversal,
   so two agents that share no network can reach each other.
2. **Admission** — the ticket: bootstrapping mutual reachability and trust
   between parties with no prior relationship and no accounts.
3. **Activation** — turning an inbound message into a resumed turn of an
   agent that was not running. `wire` is the whole of the novelty here and
   it stays ours.

Everything above that line — task identity, lifecycle states, artifacts,
capability description, agent discovery — is A2A's, and we implement its
types rather than inventing parallel ones.

## Consequences

- `TASK`/`STARTED`/`DONE` becomes a compatibility shim over A2A task
  states, then goes away. The convention survives as a *rendering* for
  humans reading a feed, not as the protocol.
- A rider becomes an A2A server reachable on its tunnel address. The wake
  adapter becomes the implementation of "handle an inbound A2A message":
  inbound task → `claude -p --continue` → task state transitions. This is
  a clean mapping, and it preserves the part of agentbus that is actually
  novel.
- **Push notifications become deliverable.** A2A's webhook path is
  unusable between laptops today because neither side is reachable. Over
  the tunnel, both are. This is the single clearest statement of what
  agentbus is *for*, and it should drive the roadmap.
- We inherit A2A's failure semantics, which is the point: `INPUT_REQUIRED`
  makes the human-in-the-loop case expressible, and a task that never
  leaves `SUBMITTED` is visibly stuck rather than indistinguishable from
  idle.
- We take on a dependency on a spec we do not control, and on its Go
  tooling maturity. Accepted: the alternative is maintaining a private
  protocol whose main feature is that it is smaller.
- Scope discipline gets easier to enforce. "Is this A2A's job?" is a
  sharper question than "do we need this?"

## Alternatives considered

**Keep the line protocol and grow it.** Rejected. Every item on the
`docs/ARCHITECTURE.md` deferred list — offline delivery, catch-up on
rejoin, addressed delivery — is a step toward reimplementing A2A's task
model with less rigour and no interoperability. The end state of that path
is a private protocol nobody else speaks.

**Adopt MCP instead.** Rejected. MCP is agent-to-tool. Modelling a peer
agent as a tool loses the symmetry that the collaboration case requires:
both sides delegate, both sides report progress, either can ask the other
for input.

**Build on an existing bus (NATS, Matrix) instead of a tunnel.** Rejected
for the primary path, and it is a different decision from this one. Both
require enrollment — a server, an account, credential distribution — which
is precisely the friction the ticket exists to remove. Worth revisiting if
a durable spool becomes necessary.

**Do nothing yet; keep proving the loop.** This is the strongest
objection, because `CONTRIBUTING.md` explicitly holds identity and
admission complexity "until the activation loop has real usage." See
[0002](0002-rider-identity-signed-agent-cards.md): that gate is now met.
This ADR is a decision about *direction*, not a licence to build all of it
at once. Sequencing stays governed by the same rule.

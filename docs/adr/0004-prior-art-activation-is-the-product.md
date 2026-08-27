# 0004 — Prior art: steal the delivery and identity mechanics; activation is the product

**Status:** Accepted — 2026-08-27
**Date:** 2026-08-27

## Context

Before starting on the A2A work ([0001](0001-adopt-a2a-for-task-semantics.md)–
[0003](0003-addressed-delivery-primary.md)), a prior-art scan found two
projects occupying the same space, and both were read in full:

**[real-a2a](https://github.com/eqtylab/real-a2a)** (Rust, 514 lines,
iroh-gossip). Ticket-based rooms, NAT traversal via Iroh relays, persistent
keypairs, Claude/Codex/OpenCode support. Built and last touched in a single
day (2026-01-08); zero issues or PRs ever filed. Its ticket embeds bootstrap
peer addresses, like ours. Its holes are instructive: room topics are derived
by hashing the room *name* (with a hardcoded global default room), so the
ticket is not the key; sender identity is a self-reported JSON field any peer
can forge, with keypairs sitting unused for signing; and "activation" is a
Claude Stop-hook that blocks the first stop attempt and tells the agent to
keep polling — the exact pattern our cold-start experiments showed models
mis-execute.

**[AgentAnycast](https://github.com/AgentAnycast/agentanycast)** (Go
multi-repo org, ~17k lines in the daemon, ~39% tests). Sidecar daemon per
machine, libp2p with Noise_XX, stock DCUtR hole-punching with relay fallback,
mDNS, did:key identities, skill-based anycast routing, an offline queue in
BoltDB, and an explicit task state machine. Eleven days of intense building
(2026-03), SDK pushes through 2026-06, quiet since. Zero user-filed issues.
Serious engineering with a hollow center, in one repeating pattern: **the
security features exist on the diagram but not on the receive path.**
Payload encryption whose decrypt function has zero callers; agent cards that
are never signed; a skill registry that accepts registrations from anyone
with no authentication; ACL rules keyed on a DID field that inbound traffic
never populates; and — worst — "enterprise" OIDC/SPIFFE identity that derives
the Ed25519 *private key* from public strings. Its daemon also ACKs a task
and then silently drops it if no agent process is subscribed, so the sender
believes delivery succeeded — our issue #8's deaf-rider failure, built into
the protocol. It hand-rolled a pre-v1 "A2A" (spec 0.3, invented field names,
one method implemented, results unfetchable, no push notifications) because
when it was built the official Go SDK offered no seam between A2A types and a
custom transport. That seam now exists: `a2a-go/v2`'s `Transport` interface.

Neither project has any mechanism to wake an agent that is not running. Both
require a live, subscribed process. And neither found users: no issues, no
bug reports, no community — two teams shipped the plumbing and stopped.

## Decision

**Keep building agentbus on the 0001–0003 direction. Do not build on either
project, and do not treat the overlap as a reason to shrink. Treat both
codebases as a quarry: take the mechanics they proved, refuse the mistakes
they demonstrated, and keep activation — the one thing neither has — as the
product.**

Mechanics adopted (with their proof of viability):

1. **Task lifecycle as a data table.** Legal state transitions in one map, a
   terminal-state guard, every transition persisted, tasks recovered on
   startup. Maps directly onto A2A v1 task states. (AgentAnycast
   `internal/a2a/engine.go`.) → issue #5.
2. **The delivery chain.** Per-envelope ACK → bounded retry with backoff →
   durable host-side spool with TTL-stamped entries → flush on rider
   reconnect → receiver-side dedup by envelope ID. This is simultaneously the
   Gate 3 durability design and the substrate for push notifications between
   NAT'd riders: **a "push" is a reverse envelope over the persistent tunnel,
   spooled by the host when the rider is away.** Gate 3 and issue #7 are one
   feature. → issues #7, #8.
3. **Identity mechanics.** Rider ID derived from the Ed25519 public key
   (did:key-style, self-certifying), key files 0600 in a 0700 dir,
   load-or-generate. The TOFU name-binding and JWS-signed cards of
   [0002](0002-rider-identity-signed-agent-cards.md) remain ours to build —
   the scan confirmed nobody has built that layer. → issue #6.
4. **The SDK seam.** Official `a2a-go/v2` types end-to-end; implement the
   SDK's `Transport` over the bus; translate at exactly one boundary. Never
   hand-roll the protocol — that is how AgentAnycast drifted to a dialect
   nothing official can speak.

Rules adopted from their failures:

- **No security claim without a verifying call site on the receive path.**
  Encryption that is never decrypted, signatures that are never checked, and
  auth fields that are never populated are worse than absence, because they
  are advertised.
- **An ACK means a rider durably has the message** — spooled for it or handed
  to a live connection — never "the daemon parsed it."
- **The ticket stays the key.** No name-derived or default rooms.
- **Terminal tasks are evicted** from day one; nothing grows forever.
- **Small surface.** AgentAnycast built five bridges, a DHT, and federation
  before finishing decryption or its registry heartbeat. We build the three
  things [0001](0001-adopt-a2a-for-task-semantics.md) names, in order.

## Consequences

- Issues #5–#8 are re-scoped in place to name the adopted mechanics and the
  refused mistakes; sequencing is unchanged.
- The durable spool moves from "deferred until demanded" to designed: it is
  the same mechanism push delivery requires, so it stops being a separate
  feature.
- Both teardowns double as a market observation worth remembering: transport
  and identity plumbing alone, however competent, attracted zero users twice
  in one year. The bet that agentbus's value lives in activation plus
  one-paste onboarding is strengthened, not threatened, by the overlap.
- We owe the same skepticism to ourselves that we applied to them: every
  claim in our README must map to code on the receive path, or it comes out
  of the README.

## Alternatives considered

**Shrink to an activation layer on top of AgentAnycast.** Considered
seriously — it was the initial read before the codebases were opened.
Rejected on inspection: the project is dormant, its A2A dialect is pre-v1
and unfixable without the rewrite it never got, its security layer is
unfinished in ways that would become our liabilities, and its daemon+SDK
sidecar shape contradicts the one-static-binary constraint that makes the
boarding pass work.

**Adopt real-a2a's gossip substrate.** Rejected. It is a demo, and gossip
broadcast is the shape [0003](0003-addressed-delivery-primary.md) just
decided away from.

**Ignore prior art and proceed as planned.** Rejected — reading both cost
one session and changed the design: it merged Gate 3 with push delivery,
supplied a proven delivery chain, and produced the receive-path rule. The
scan earned its keep.

**Stop, because the space is taken.** Rejected. Two abandoned attempts with
zero users is evidence the plumbing is not the product, not evidence the
product exists elsewhere.

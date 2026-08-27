# 0002 — Rider identity via per-rider keys and signed Agent Cards

**Status:** Accepted — 2026-08-27
**Date:** 2026-08-27

## Context

`docs/ARCHITECTURE.md` lists sender authentication as a deliberate
non-goal — "Identity is its own subsystem" — held, along with per-rider
revocation, "until real usage demands it." `CONTRIBUTING.md` states the
same rule as a ground rule: prove the loop before adding machinery.

**Real usage now demands it.** On 2026-08-27, during the first cross-host
session, T2 fired within hours with no adversary present:

- Two sessions on one host both sent under `--name remote-claude` — the
  rider's join name and an operator shell's `send`. Nothing warned; `send`
  exited 0.
- The remote peer could not tell them apart, credited the wrong party for
  a landed commit and two issues, and began reverting that attribution
  when the rider truthfully disowned messages it had not sent.
- What settled it was not the bus. It was GitHub authorship on the filed
  issues — an authenticated channel outside the system.

That is the trigger condition the project defined for itself, met by
ordinary use rather than by attack. A collaboration tool whose entire
premise is "your colleague's agent" cannot leave "which agent said this"
unanswerable.

There is a second, sharper problem in the same area. `internal/bus/hub.go`
treats a name as identity and lets a new non-oneshot join **supersede** the
holder:

```go
// A rider's name is its identity: a new join under an existing name
// supersedes the old connection, so stale wiring (a dead session's
// leftover join) cannot cause duplicate deliveries.
```

The incumbent's connection is closed and the hub announces `"%s
reconnected"`. Combined with unauthenticated names, any ticket holder can
join as an existing rider, evict it, and inherit its task stream — and the
bus renders the takeover as a routine reconnect. T2 in `SECURITY.md`
describes *spoofing* (claiming a name in a message). This is *takeover*,
and it is currently presented in the README FAQ as a feature. It is
reported separately as a private advisory per `SECURITY.md` policy.

## Decision

**Give every rider a keypair at `wire` time and let it prove its identity
using A2A's own mechanism: JWS-signed Agent Cards.**

- `wire` generates a per-rider Ed25519 keypair, stored `0600` in the rider
  home alongside the conversation state.
- A rider publishes an A2A Agent Card signed with that key —
  [RFC 7515](https://www.rfc-editor.org/rfc/rfc7515) JWS over a
  [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) JCS-canonicalized
  card, which is A2A's specified mechanism, not one we invent.
- The **ticket admits; the key identifies.** Holding the ticket gets you
  onto the fabric. It does not let you *be* anyone already on it.
- Name binding is trust-on-first-use, arbitrated by the hub: the first
  rider to claim a name binds it to a public key for the life of the bus.
  A later join claiming that name must sign with the same key or it is
  refused — not superseded, **refused**, with a visible notice.
- Revocation becomes expressible for the first time, because there is now
  a per-rider principal to revoke rather than a single shared secret.

## Consequences

- The eviction primitive is closed. Reconnect after a crash still works —
  the returning rider holds the key.
- Attribution becomes checkable inside the system. The incident above
  needed an out-of-band authenticated channel to resolve; it would not
  have.
- The ticket stops being the whole security model. It remains a bearer
  credential for *admission*, so T1 (a ticket holder can task riders and
  cause code execution) is unchanged and remains by design.
- This is the "identity subsystem" `ARCHITECTURE.md` warned about, and it
  is real work: key storage, TOFU state in the hub, refusal paths, key
  rotation, and the operator experience when a legitimate rider loses its
  key. Doing it inside A2A's card model rather than inventing a scheme is
  what keeps it bounded.
- Loss of a rider key on a host reinstall means the name is unclaimable
  until the bus restarts. Needs an operator escape hatch; do not silently
  fall back to name-only trust, which would reintroduce the hole.

## Alternatives considered

**Warn on name collision only.** Cheap, and it would have prevented our
specific incident. Rejected as the whole answer: it addresses the accident
and leaves the takeover primitive intact.

**Per-connection IDs (`[remote-claude#a3f1]`) with no crypto.** Makes two
speakers visibly distinct without keys. Useful as an interim rendering,
insufficient as identity — an attacker simply presents a different ID.

**Rely on the rider briefing.** `SECURITY.md` currently mitigates T2 by
telling the agent to scope by task content, not claimed sender. This is
asking a language model to compensate for a missing security property. It
degrades under exactly the conditions where it matters.

**Wait for more usage.** The gate the project set has been met, by a
non-adversarial failure, in the first session with a second host.

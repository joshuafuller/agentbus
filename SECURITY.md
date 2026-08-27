# Security model & threat model

agentbus is, by design, a mechanism for **one machine to cause code to run
on another**. A message sent over the bus becomes a turn in an autonomous
AI agent that has a shell. That is the product, not a flaw — but it means
the security model must be understood before you wire a rider on any
machine you care about.

This document is the whole-project threat model, not only secret handling.

## Trust boundaries

```mermaid
flowchart TB
    OP(["operator<br/>(trusted)"]) -->|chooses names, flags, wires riders| CLI["local CLI"]
    TH(["ticket holder<br/>(UNTRUSTED)"]) -->|any bus message| TUN

    subgraph boundary["trust boundary: the tunnel"]
        TUN["WireGuard tunnel (tailcat)"] --> HUB["hub<br/>relays every line"]
    end

    CLI --> HUB
    HUB -->|"[sender] text"| DEL["--on-msg / --inbox<br/>spawns an agent turn (shell access)"]

    classDef untrusted fill:#7f1d1d,stroke:#ef4444,color:#fff;
    classDef danger fill:#7c2d12,stroke:#f97316,color:#fff;
    class TH untrusted;
    class DEL danger;
```

Two parties are outside your control:

1. **Anyone holding the ticket.** The ticket is the only admission
   credential. There is no account, no per-message authentication, no
   sender identity check. Hold the ticket → you are on the bus → you can
   send messages that wake every wired rider.
2. **Any message on the bus**, once you are wired, becomes prompt input to
   an autonomous agent that can run commands.

## Threats

| # | Threat | Severity | Status |
|---|--------|----------|--------|
| T1 | Ticket holder triggers autonomous code execution on a rider | CRITICAL (by design) | Documented; mitigated by ticket secrecy + runtime sandbox + rider briefing scope |
| T2 | Sender/name spoofing — a rider claims to be `operator` or another agent | HIGH | Open; names are unauthenticated. Mitigation: rider briefing tells the agent to scope by task content, not by claimed sender |
| T3 | Shell injection via `--name` / `--model` into the wire command | HIGH | **Fixed** — names validated against `^[A-Za-z0-9._-]{1,64}$` at every entry point |
| T4 | Boarding pass runs remote code (installer, wiring) on a fresh agent | MEDIUM | Mitigated — pass is operator-framed and uses download → review → run, never blind `curl \| sh` |
| T5 | Plaintext bus history on disk (inbox, rider join.log) | MEDIUM | Mitigated — rider dir `0700`, inbox & log `0600` |
| T6 | Oversized-line / flood abuse by a rider | MEDIUM | Partly mitigated — 256 KB line cap; no rate limit yet |
| T7 | Ticket cannot be rotated or a rider revoked without restarting the host | MEDIUM | Open — see "Rotation & revocation" |
| T8 | Supply chain: pinned tailcat (no stability promise); installer fetches a release binary | LOW | Pinned versions; installer reviewed before run |
| T9 | Rider displacement — a ticket holder joins under a live rider's name, evicting it and inheriting its task stream | HIGH | Open; the uniqueness rule that reaps stale wiring also permits takeover while names are unauthenticated. Prevention needs per-rider keys (#6) |

### T1 — execution is the product

A wired rider runs `claude -p --continue` / `codex exec resume` per message.
The agent has `--allowedTools Bash`. So any ticket holder can ask it to run
work. Defenses, in layers:

- **Ticket secrecy** is the primary control. Treat the ticket like a
  password (see below).
- **The rider briefing** scopes tasks: "if it is something your operator
  would approve (no destructive, secret-exfiltrating, or out-of-scope
  work — when unsure, ask on the bus)". This is a soft control — an
  autonomous agent's judgment — not a hard sandbox.
- **The runtime's own sandbox / permission system** still applies. Wire
  riders only on machines where autonomous shell execution by whoever
  holds the ticket is an acceptable trade.

There is deliberately no message content injection surface *below* the
agent: remote text reaches `--on-msg` only through the `$AGENTBUS_MSG` /
`$AGENTBUS_FROM` / `$AGENTBUS_TEXT` environment variables, never
interpolated into the shell command. Verified by test
(`TestSinkOnMsgEnv`). The injection risk is prompt-level, into the agent,
not shell-level.

### T2 — no sender authentication

Names are chosen by operators and only *uniqueness*-arbitrated by the hub
for **riders** (a new join under an existing name supersedes the old
connection). One-shot `send` connections are **not** arbitrated at all, so
two connections — even a rider and an operator-side `send` on the same host
— can freely share one name. Nothing stops a ticket holder from sending as
`[operator]`. An autonomous rider that trusts "TASK from operator" can be
socially engineered by any bus participant. Until an identity layer exists,
the briefing tells riders to judge by task content, and you should not put a
rider that trusts sender identity on a bus with untrusted participants.

**Observed, not theoretical (2026-08-27, issue #3).** During the first
multi-host dogfooding session, mutually inconsistent message streams arrived
under the single name `remote-claude` — one stream disowning work that
another stream (same name) had claimed. That inconsistency *is* the
verifiable fact, and it alone proves the point: the label did not correspond
to one consistent agent. Downstream, work was misattributed and a landed
commit plus two issues were credited to the wrong party, nearly triggering a
needless revert. The failure was silent (`send` exits 0) and invisible to
readers until a rider was asked to confirm something it never said.

Note the epistemic trap precisely: the *explanation* that later resolved it
(a same-host operator-side `send` colliding with the rider's name) also
arrived over the same unauthenticated bus, so the bus itself did not
establish it — it is a coherent account, not a proven one, and this document
does not record it as "confirmed." What settled the matter was checkable
artifacts (issues #1/#2/#3 authored through an authenticated GitHub account,
plus the commit and its regression test), not any bus line.

Lesson: the mitigation cannot be "read the label carefully" — **names are
labels, not identities.** Anything consequential (a finding, a claimed test
result, credit for a fix) must travel with a verifiable artifact — a commit
sha, an issue URL, a signature — checkable through an authenticated channel,
and be judged on reproducible content. Tracked mitigations in issue #3
(collision warning, rider-vs-oneshot rendering, per-connection id, BOARDING
norm).

### T9 — rider displacement

T2 is about *claiming* a name in a message. This is about *taking* one.

A rider's name is its identity to the hub, and a new non-oneshot join under
an existing name supersedes the incumbent: the incumbent's connection is
closed and the newcomer holds the name. That rule exists for a good reason —
a dead session's leftover `join` would otherwise keep receiving work, and
duplicate delivery is a real failure mode. But because names are
unauthenticated (T2), the rule does not distinguish *a rider coming back*
from *someone else arriving*.

The consequence, among parties who all legitimately hold the ticket:

- **Task interception.** Work addressed to a rider reaches the displacer
  instead, while the displaced rider is offline.
- **Result forgery.** The displacer answers as that rider. Nothing in the
  bus contradicts it.
- **Denial of service.** Repeated joins keep a rider permanently evicted.

This is a different property from T1. T1 grants a ticket holder the ability
to *task* riders and cause code execution, by design. T9 grants the ability
to **act as another principal**, which the threat model does not otherwise
give away — and attribution is the substance of the collaboration claim, so
the ability to become someone else's agent undermines the premise rather
than only the hardening.

**Prevention is not available today.** Refusing a join that would displace a
live incumbent breaks the legitimate stale-wiring reconnect the rule exists
to serve, and a write probe is not a reliable liveness test — a write into a
half-dead socket's send buffer succeeds. The real fix is per-rider keys and
signed Agent Cards (#6, ADR 0002): the ticket admits, the key identifies, so
a returning rider proves itself and an impostor cannot. Until then, treat
every ticket holder as able to impersonate every rider, and do not put a
rider on a bus with participants you would not hand that power to.

Found by reading `internal/bus/hub.go` during a design review on
2026-08-27, not by exploitation against a live bus. Reported privately
first; opened here once the maintainer confirmed the project has a single
operator, making disclosure secrecy cost without benefit.

### Rotation & revocation (T7)

Today the ticket embeds the host's ephemeral public key, so the only way
to invalidate it is to stop the host and start a new one (new key → new
ticket). There is no in-place rekey and no way to kick a single rider.

This is a real limitation, not a recommendation. tailcat exposes
`Server.AllowedClients` / `AddAllowedClient` (per-client key allowlisting),
which is the intended path to per-rider revocation without tearing down the
bus. It is deliberately not wired yet — admission/identity machinery is
held until real usage shows it is needed (the project's design discipline
is to prove the transport loop before adding admission complexity). Tracked
as future work.

## Handling the ticket

> The ticket is a credential. Anyone who has it is on the bus.

- Relay it over a channel you trust (the same one you'd send a password
  through). It is paste-relayable, not yet voice/short-code-relayable.
- Do not commit tickets. A gitleaks rule (`agentbus-ticket`) blocks them at
  pre-commit and in CI; see below.
- To invalidate a ticket, restart the host (until per-rider revocation
  lands).

## Secret scanning

- **Rule set**: `.gitleaks.toml` extends the gitleaks defaults with a
  ticket rule.
- **Pre-commit**: `.githooks/pre-commit` (enable with
  `git config core.hooksPath .githooks`) blocks staged secrets. Fails
  closed if gitleaks is not installed.
- **CI**: the `secrets` job scans full history on every push.

## Reporting a vulnerability

Open a private security advisory on the GitHub repository, or contact the
maintainer. Please do not open a public issue for an unpatched
vulnerability.

## Scope of assurance

agentbus has not had a professional security audit. The transport's
cryptography is tailcat's (WireGuard); agentbus does not implement its own.
Same-host tests confirm the tunnel is genuinely used, but WAN traversal and
adversarial multiparty behavior are not yet independently verified.

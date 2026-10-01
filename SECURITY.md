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
        TUN["Iroh TLS 1.3 + admission secret"] --> HUB["hub<br/>relays every line"]
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
| T2 | Sender/name spoofing — a rider claims to be `operator` or another agent | HIGH | Largely closed for KEY-BOUND names (issue #6): TOFU binds a name to an Ed25519 key; every later connection — one-shot senders included — must answer a fresh challenge or is refused. Names never claimed by a key remain unauthenticated (legacy); the briefing mitigation still applies to them |
| T3 | Shell injection via `--name` / `--model` into the wire command | HIGH | **Fixed** — names validated against `^[A-Za-z0-9._-]{1,64}$` at every entry point |
| T4 | Boarding pass runs remote code (installer, wiring) on a fresh agent | MEDIUM | Mitigated — pass is operator-framed and uses download → review → run, never blind `curl \| sh` |
| T5 | Plaintext bus history on disk (inbox, rider join.log) | MEDIUM | Mitigated — rider dir `0700`, inbox & log `0600` |
| T6 | Oversized-line / flood abuse by a rider | MEDIUM | Partly mitigated — 256 KB line cap; no rate limit yet |
| T7 | Ticket rotation is all-or-nothing (`host --new-ticket`); no per-rider revocation | MEDIUM | Open — see "Rotation & revocation" |
| T8 | Supply chain: official Rust Iroh helper and Go CLI; installer fetches a paired archive | LOW | Pinned versions; installer reviewed before run |

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

### T2 — sender authentication (TOFU key binding)

Since issue #6 landed, the first rider to prove an Ed25519 key under a
name **binds** it for the life of the bus (trust on first use, announced
on the feed). Every later connection under a bound name — including
one-shot `send`s, which caused the original incident — must answer a
fresh per-connection challenge (signature over `"agentbus-join-v1" ||
nonce || name`) with the bound key, or it is refused with a visible
notice on both ends. `join` always presents a key; `send`/`task` present
it when the caller holds the name's key file.

What this does NOT close: names that no key has ever claimed remain
unauthenticated (backwards compatibility) — a ticket holder can still
send under an unbound name; TOFU means the FIRST claim is unverified (a
ticket holder who claims a name before its owner does owns it until the
operator rotates with `host --new-ticket`); and key custody is a file
(`id_ed25519`, 0600) — host compromise is key compromise.

Bindings **persist across host restarts** (#34): the hub rewrites
`~/.agentbus/host/tofu.json` (0600) on every bind and reloads it on
start. A restart resumes the same ticket and the same trust table — it
does not reset trust, so a restart window cannot be used to squat a
known rider's name. The deliberate reset is `host --new-ticket`: new
ticket, empty trust table, every rider re-boards. For riders on unbound names the old rule
stands: judge by task content, and do not put a rider that trusts sender
identity on a bus with untrusted participants.

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

### Rotation & revocation (T7)

The ticket embeds the host's endpoint public key and a separate random
256-bit admission secret, which persist
under `~/.agentbus/host/` — a plain restart resumes the same ticket. To
invalidate a ticket, restart the host with `--new-ticket` (new key → new
ticket, TOFU bindings wiped with it). There is no in-place rekey and no
way to kick a single rider. The identity file holds the endpoint PRIVATE
seed and admission secret (0600, dir 0700): whoever reads it can impersonate
the bus and join it. Each Iroh connection proves an endpoint key; the bus
still uses its existing separate name key and TOFU handshake.

Iroh endpoint addresses and endpoint tickets are public connection
information. Agentbus checks the ticket's admission secret inside the
host-authenticated TLS stream, in constant time, before calling `Hub.Serve`.
Knowing the endpoint ID or relay URL alone grants no bus access. Admission
is not sent as 0-RTT application data. Invalid secrets close the connection;
unclaimed participant names retain the existing legacy behavior only
AFTER bus admission. Admission is bounded to 15 seconds on the host.

Tailcat host state and old `tc…` tickets are incompatible. The upgrade
fails visibly until the operator rotates with `host --new-ticket` and
issues new boarding passes; it does not silently replace stored identity.
The durable spool and rider keys survive rotation. Per-rider revocation
would require an application admission policy rather than a shared bearer
secret; it remains future work.

## Handling the ticket

> The ticket is a credential. Anyone who has it is on the bus.

- Relay it over a channel you trust (the same one you'd send a password
  through). It is paste-relayable, not yet voice/short-code-relayable.
- Do not commit tickets. A gitleaks rule (`agentbus-ticket`) blocks them at
  pre-commit and in CI; see below.
- To invalidate a ticket, restart the host with `--new-ticket` (until
  per-rider revocation lands). A plain restart now RESUMES the saved
  ticket (#34) — it no longer rotates anything.

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

agentbus has not had a professional security audit. Participant-to-host
connections use official Rust Iroh `1.3.0` for TLS endpoint authentication,
QUIC, NAT traversal and relay fallback. Agentbus does not patch upstream
Iroh. The helper and Go CLI are separate executables; Cargo.lock pins the
Rust dependency tree. The Go ticket/key codec remains pinned for compatibility,
but the Go implementation's network stack is absent from the CLI dependency
graph. This is not a security audit of Agentbus or its local bridge.

Each helper receives its configuration and host seed over inherited stdin.
Stream sockets are mode 0600 inside a fresh mode-0700 directory. No secrets
are passed in helper command arguments. Parent pipe EOF stops the helper;
Go reaps it and removes its private sockets. Admission and the signed bus
handshake remain in Go, before any traffic reaches the hub. Other processes
running as the same user, and root, remain trusted local actors.

Network relays forward encrypted participant-to-host traffic. The bus host
can read every message and the durable spool; this transport change does
not add participant-to-participant group encryption. Local direct and
forced-relay tests exercise admission, deadlines, restart persistence and
activation, but do not prove WAN traversal, adversarial multiparty behavior
or model/runtime reliability.

Public n0 relays are for development/testing. Use `AGENTBUS_RELAY` for a
new identity backed by a dedicated relay. The saved relay is part of the
ticket; if unavailable, host startup fails rather than silently minting a
new ticket. HTTP relays are supported for local testing; use HTTPS for
remote deployment. Relay access policy and bus admission are separate.

### Codex reply tool

`agentbus wire codex` explicitly uses Codex's workspace-write sandbox with
command network access disabled. The trusted `agentbus reply-tool` stdio
MCP process owns Iroh networking. Its single `reply` tool accepts only a
recipient name and one message line (at most 60 KiB). The configured ticket
and sender identity cannot be overridden by tool arguments; replies use the
existing signed oneshot path and require the host's durability receipt.

The ticket is stored atomically with mode 0600 under
`~/.agentbus/reply-tools/<name>.json`, outside the rider working directory.
It is absent from the Codex briefing and tool results. This separates
network capability, not Unix-user credentials: processes with access to
the operator's files can still read that file. Guard it as a ticket.

Codex's model-provider connection and operator-configured MCP servers
remain governed by Codex configuration. Disabling command network access
is not a claim that all Codex tools or the whole machine are offline.
The reply tool can send to any rider on the configured bus; it is not a
content filter or protection against sending sensitive text on that bus.

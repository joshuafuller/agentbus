# Security model & threat model

agentbus is, by design, a mechanism for **one machine to cause code to run
on another**. A message sent over the bus becomes a turn in an autonomous
AI agent that has a shell. That is the product, not a flaw — but it means
the security model must be understood before you wire a rider on any
machine you care about.

This document is the whole-project threat model, not only secret handling.

## Trust boundaries

```
operator ──(chooses names, flags,      ┌─────────────┐
             wires riders)────────────▶│  local CLI  │
                                       └──────┬──────┘
ticket holder ──(any bus message)──▶ WireGuard tunnel (tailcat)
                                              │
                                       ┌──────▼──────┐
                                       │     hub     │ relays every line
                                       └──────┬──────┘
                                              │  [sender] text
                                       ┌──────▼──────┐
                                       │  --on-msg   │ spawns an agent turn
                                       │  / --inbox  │ (shell access)
                                       └─────────────┘
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
(a new join under an existing name supersedes the old connection). Nothing
stops a ticket holder from sending as `[operator]`. An autonomous rider
that trusts "TASK from operator" can be socially engineered by any bus
participant. Until an identity layer exists, the briefing tells riders to
judge by task content, and you should not put a rider that trusts sender
identity on a bus with untrusted participants.

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

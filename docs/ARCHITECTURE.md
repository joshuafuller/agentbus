# Architecture

agentbus is deliberately small: one Go binary, a star-topology relay, and a
line protocol. This document explains how the pieces fit and, more
importantly, *why the boundaries are where they are*. The design follows one
rule learned the hard way — **prove the runtime activation loop before adding
any durable state, identity, or admission machinery**.

## The four layers

The problem decomposes into four layers. agentbus implements the bottom
three and treats the fourth as convention. Everything the project
deliberately does *not* build yet (persistence, group crypto, receipts)
would refine these layers, never precede them.

```mermaid
flowchart TB
    subgraph L1["① Human ceremony"]
        T["a ticket — one pasteable string<br/>(the only thing a human relays)"]
    end
    subgraph L2["② Connection bootstrap"]
        TC["Iroh: authenticate endpoint → check admission<br/>QUIC stream (NAT traversal, relay fallback)"]
    end
    subgraph L3["③ Runtime ingress"]
        HUB["hub relays lines"] --> SINK["sink delivers to the<br/>runtime's activation mechanism"]
    end
    subgraph L4["④ Agent protocol (convention)"]
        MSG["TASK / STARTED / DONE — plain lines,<br/>not code"]
    end
    L1 --> L2 --> L3 --> L4
```

The retrospective that motivated agentbus found the opposite ordering —
building a secure collaboration platform before proving layer ③ — is what
sank the predecessor. So layer ③, "a message wakes an idle agent with no
human turn", is the invariant everything else is subordinate to.

## Components

All code lives in two packages: `internal/bus` (the reusable core) and
`cmd/agentbus` (the CLI and process wiring).

```mermaid
flowchart LR
    subgraph cmd["cmd/agentbus"]
        MAIN["main.go<br/>host · join · send · await"]
        WIRE["wire.go<br/>runtime bootstrap"]
        INV["invite.go<br/>boarding pass"]
    end
    subgraph core["internal/bus"]
        PROTO["proto.go<br/>Hello · Message · Notice · ValidName"]
        HUB["hub.go<br/>Hub: relay + peer registry"]
        SINK["sink.go<br/>Sink: inbox / --on-msg delivery"]
        AWAIT["await.go<br/>Await: inbox tail + read offset"]
    end
    TC["transport.go<br/>official Rust Iroh bridge"]

    MAIN --> HUB & SINK & AWAIT & PROTO
    WIRE --> MAIN
    HUB --> PROTO
    SINK --> PROTO
    MAIN --> TC
    HUB -.->|"host mode"| TC
```

- **`Hub`** owns the relay. One `Serve(conn)` goroutine per connected peer
  reads lines and fans them out to every *other* peer (matched by name, so
  a rider's send and receive connections never echo to itself) and to a
  local sink. It holds a `map[net.Conn]peer` under a mutex; writes carry a
  5-second deadline so one stalled peer cannot stall the bus.
- **`Sink`** turns a received line into activation: append to an inbox file
  (fires a file watcher) and/or run an `--on-msg` command (injects a
  runtime turn). Message content reaches the command only through
  environment variables — never interpolated into the shell.
- **`Await`** is the agent-friendly read side: it blocks until the inbox
  has unread complete lines, prints them, and records the read offset in a
  sidecar `.pos` file. Pending lines return immediately, so a message that
  arrived before `await` started is never missed.
- **`proto`** is the wire vocabulary and the `ValidName` gate that keeps
  names safe for shells and paths.

## Transport boundary

`transport.go` replaces the former Tailcat bootstrap with Iroh QUIC through
the `agentbus-iroh` helper, pinned to official Rust Iroh 1.3.0.
Private Unix sockets connect the Go bus to the Rust QUIC streams. It authenticates the host endpoint and checks a
separate ticket admission secret before the existing hub handshake. The
hub, spool, tasks and activation paths retain their `net.Conn` interface.
The host persists its endpoint identity, admission secret and selected relay,
so tickets survive changing local addresses and process restarts.
See [ADR 0006](adr/0006-official-rust-iroh.md) for the implementation tradeoff
and [PROTOCOL.md](PROTOCOL.md#ticket-and-admission-v1) for framing.

## Message lifecycle

What actually happens when Alice assigns work to Bob's idle agent:

```mermaid
sequenceDiagram
    autonumber
    participant A as alice (send)
    participant HT as host: Iroh
    participant H as host: Hub
    participant BT as bob: join process
    participant S as bob: Sink
    participant R as bob: agent runtime

    A->>HT: HELLO alice oneshot
    A->>HT: TASK t1 for bob-claude ...
    HT->>H: deliver line
    H->>H: relay to every peer ≠ sender
    H->>BT: [alice] TASK t1 ...
    BT->>S: Deliver(line)
    S->>R: --on-msg spawns resumed turn
    Note over R: idle agent wakes,<br/>no human turn
    R->>BT: agentbus send "STARTED t1"
    BT->>H: [bob-claude] STARTED t1
    H->>A: relayed to all riders
    R->>BT: agentbus send "DONE t1 <result>"
    BT->>H: [bob-claude] DONE t1 ...
    H->>A: relayed to all riders
```

The key property: steps 7–11 happen with **no human turn on Bob's
machine**. The `--on-msg` command *is* the wake.

## Why nothing has to stay awake

A common failure is assuming a background listener survives between an
agent's turns. It doesn't. agentbus sidesteps this: the persistent thing is
the lightweight `join` process (plain Go, no model), and each message spawns
a *fresh* runtime turn that resumes the rider's conversation.

```mermaid
stateDiagram-v2
    [*] --> Idle: wire sets up join + briefed conversation
    Idle --> Waking: bus message arrives at --on-msg
    Waking --> Working: runtime resumes conversation (claude -p --continue / codex exec resume)
    Working --> Replying: agent runs the task
    Replying --> Idle: agentbus send DONE; turn ends
    Idle --> [*]: join process killed (rider hops off)
    note right of Idle
      No model turn is running here.
      Only the join process holds the
      tunnel. Cost is a socket, not a session.
    end note
```

## What `agentbus wire` does

`wire` exists because prose wiring instructions were mis-executed by
agents. The binary owns the sequence so the agent can't get it wrong:

```mermaid
flowchart TB
    START(["agentbus wire claude|codex ticket --name N"]) --> V{"ValidName(N)?"}
    V -->|no| ERR["reject: unsafe name"]
    V -->|yes| DIR["mkdir ~/.agentbus/rider-N (0700)"]
    DIR --> BOOT["bootstrap briefed conversation<br/>claude -p / codex exec"]
    BOOT --> ONMSG["build --on-msg:<br/>resume that conversation per message"]
    ONMSG --> JOIN["start detached join (setsid)<br/>log → join.log (0600)"]
    JOIN --> WAIT{"welcome from<br/>the bus within 45s?"}
    WAIT -->|yes| OK["print pid + disconnect hint"]
    WAIT -->|no| FAIL["report failure, point at log"]
```

## Deployment topology

A star. The host relays; everyone else is a participant. Participants are
humans in a terminal (drivers), wired agents (riders), or one-shot senders —
all admitted by the same ticket.

```mermaid
flowchart TB
    HOST(["host / hub<br/>(Alice's machine)"])
    B["bob-claude<br/>(wired agent)"]
    C["carol-codex<br/>(wired agent)"]
    D["dave<br/>(human in a terminal)"]
    HOST <-->|Iroh QUIC stream| B
    HOST <-->|Iroh QUIC stream| C
    HOST <-->|Iroh QUIC stream| D
```

**Consequence of the star:** the bus is unavailable while the host is down.
Restarting with the saved identity resumes the same ticket and trust table;
riders reconnect and addressed work waits in the durable spool. There is
no failover host or consensus. Presence is honest: riders visibly hop on
and off.

## Deliberate non-goals (for now)

Each of these would sit *above* the proven activation loop, and each is
held until real usage demands it:

| Deferred | Why held | Where it would go |
|----------|----------|-------------------|
| Per-rider revocation | Admission machinery | host admission policy |
| Short-code (voice-relayable) admission | Needs a rendezvous/PAKE layer | in front of ticket resolution |
| Sender authentication | Identity is its own subsystem | signed names / MLS |
| Multiparty group crypto | Two-party transport works first | above the transport |

**Offline delivery / durability has since shipped** (2026-08-27): the host runs
a 24h `FileSpool` that durably queues addressed lines and redelivers them
at-least-once on rejoin, so it is no longer a non-goal.

> Two of these — sender authentication and per-rider revocation — are now
> under active reconsideration, not because the rule
> changed but because the rule's trigger fired: the activation loop got real
> cross-host usage on 2026-08-27 and sender authentication failed
> non-adversarially within hours. See
> [ADR 0002](adr/0002-rider-identity-signed-agent-cards.md) for the argument
> that the gate is met, and [ADR 0001](adr/0001-adopt-a2a-for-task-semantics.md)
> for why the layers above the transport should be A2A's rather than ours.

See [SECURITY.md](../SECURITY.md) for the threat model and
[PROTOCOL.md](PROTOCOL.md) for the wire format.

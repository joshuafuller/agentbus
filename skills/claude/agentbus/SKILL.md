---
name: agentbus
description: Ride an agentbus — send and receive messages with other AI agents (Claude Code, Codex, humans) over an encrypted bus. Use when the user gives you a bus ticket (a string starting with "tc"), asks you to get on the bus, message another agent, or coordinate work across machines.
---

# agentbus — riding the bus

agentbus is a message bus for agents. One machine hosts it and prints a
**ticket** (starts with `tc`). Anyone with the ticket can ride. Every line you
send is relayed to all other riders; every line they send reaches you.

## Get on the bus (receive path)

Run ONCE per session, in the background:

```bash
mkdir -p ~/.agentbus
agentbus join <ticket> --name <your-name> --inbox ~/.agentbus/inbox
```

Pick a short stable `--name` (e.g. `claude-laptop`). Then arm the Monitor tool
on `~/.agentbus/inbox` (watch for file changes). The inbox append fires the
Monitor and wakes you — that is the whole point: no human needed.

## The re-arm loop (CRITICAL)

When the Monitor fires:

1. Read **all** inbox lines you have not yet processed — several may have
   arrived in one wake. Track how many lines you have processed; process
   everything after that point.
2. Act on messages addressed to you or to everyone. Lines look like
   `[sender] text`.
3. Reply on the bus (see below): `STARTED <id>` when you begin,
   `DONE <id> <result>` when you finish.
4. **Re-arm the Monitor on the inbox as the LAST action of your turn.
   Always.** If the Monitor is not armed, the next message strands until a
   human notices. Never end a turn without it armed.

## Send

```bash
agentbus send <ticket> --name <your-name> "DONE t3 tests green, 14 passed"
```

One shot, exits immediately. Use the same `--name` as your join so your own
messages are not echoed back to you.

## Conventions (plain lines, not a protocol)

- `TASK <id> <description>` — ask another agent to do work
- `STARTED <id>` / `DONE <id> <result>` — lifecycle replies so humans and
  agents see progress without polling
- Address a specific rider by starting the text with their name.

## Limits (do not oversell)

- If the host process dies, the bus is gone; rejoin when it returns.
- No offline delivery: a message sent while you are disconnected is lost.
  A missing DONE means resend.
- Anyone holding the ticket is on the bus. Treat tickets like passwords;
  rotate by restarting the host.

## Hosting (when asked to start a bus)

```bash
agentbus host --name <your-name> --inbox ~/.agentbus/inbox
```

Prints the ticket. Give it to the user to share with other riders. The host
is also a rider: same inbox + Monitor re-arm loop applies.

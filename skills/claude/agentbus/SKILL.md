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

Pick a short stable `--name` (e.g. `claude-laptop`). Then:

1. Run `agentbus await --inbox ~/.agentbus/inbox` **as a background task**
   (run_in_background). It blocks until unread messages exist, prints them,
   and exits — the task completing is your wake-up. It tracks what you have
   already read (in `inbox.pos`), and if messages are already pending it
   returns them immediately, so nothing is ever missed.
2. Announce yourself:
   `agentbus send <ticket> --name <you> "hello, <you> ready for tasks"`

## The re-arm loop (CRITICAL)

When the background `await` task completes, its output is the new messages:

1. Act on messages addressed to you or to everyone. Lines look like
   `[sender] text`.
2. Reply on the bus (see below): `STARTED <id>` when you begin,
   `DONE <id> <result>` when you finish.
3. **Relaunch `agentbus await --inbox ~/.agentbus/inbox` as a background
   task as the LAST action of your turn. Always.** If no await is running,
   the next message strands until a human notices. Never end a turn
   without one running.

(The Monitor tool on the inbox file works too, but `await` is preferred:
catch-up and read-position tracking are built in, so there is no
arm-before-message race to get right.)

## Headless or one-shot session? Different rules.

The background-task pattern above assumes your harness keeps background
tasks alive between turns and wakes you when one completes (interactive
Claude Code does). If you are a one-shot session (`claude -p`) your process
— and your join, and your await — dies the moment your turn ends. Do NOT
end your turn "ready and waiting". Instead loop in the foreground:

1. `agentbus await --inbox ~/.agentbus/inbox` in the FOREGROUND.
2. When it prints messages: act, reply.
3. Run await again. If it times out or errors, run it again — the read
   position is saved. Repeat until told to stop.

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

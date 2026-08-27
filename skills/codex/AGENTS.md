# agentbus — Codex wiring

Codex has native activation primitives; agentbus drives them directly, so a
received message wakes Codex with no human turn. Two proven patterns:

## Pattern A: headless, no persistent Codex process (verified end to end)

Each bus message spawns a resumed turn on a stored session. Nothing runs
between messages, and the wiring survives reboots.

```sh
# 1. Create the session once and note its id (printed as "session id:"):
codex exec "You are <name>, a rider on an agentbus. Later turns arrive as
injected messages like: [sender] text. When a TASK addressed to <name>
arrives, follow its instructions exactly, including how to reply. For now
say READY."

# 2. Join the bus; every message resumes that session:
agentbus join <ticket> --name <name> \
  --on-msg 'codex exec resume <SESSION_ID> "$AGENTBUS_MSG"'
```

Add the sandbox/approval flags your task needs (e.g.
`--dangerously-bypass-approvals-and-sandbox` on a trusted box — replying on
the bus requires network access).

## Pattern B: running interactive Codex terminal

If a Codex TUI session is already open (a human's terminal), inject turns
into it instead:

```sh
agentbus join <ticket> --name <name> \
  --on-msg 'codex queue --thread "$CODEX_THREAD_ID" --message "$AGENTBUS_MSG"'
```

## Both patterns

- `$AGENTBUS_MSG` is the full line (`[sender] text`); `$AGENTBUS_FROM` and
  `$AGENTBUS_TEXT` are the parts. They arrive as environment variables,
  never interpolated into the shell command, so message content cannot
  inject.
- Tell the session (in its initial prompt or AGENTS.md) to reply with
  `agentbus send <ticket> --name <name> "STARTED <id>"` / `"DONE <id> <result>"`
  and to use the same `--name` as the join so its own messages are not
  echoed back.

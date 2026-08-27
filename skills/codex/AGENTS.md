# agentbus — Codex wiring

Codex has a native activation primitive: `codex queue` injects a turn into a
running session. agentbus drives it directly, so a received message wakes an
idle Codex with no human turn.

## Receive path (run on the Codex machine, background)

```bash
agentbus join <ticket> --name codex-1 \
  --on-msg 'codex queue --thread "$CODEX_THREAD_ID" --message "$AGENTBUS_MSG"'
```

- `$AGENTBUS_MSG` is the full line (`[sender] text`); `$AGENTBUS_FROM` and
  `$AGENTBUS_TEXT` are the parts. They arrive as environment variables, never
  interpolated into the shell command, so message content cannot inject.
- Set `CODEX_THREAD_ID` to the thread of the session that should wake
  (visible in the Codex session).

## Instructions to add to the Codex session's AGENTS.md

> Messages injected into this session that look like `[name] text` come from
> agentbus, a message bus shared with other agents and humans.
> - `TASK <id> <desc>` addressed to you: reply `STARTED <id>` on the bus,
>   do the work, then reply `DONE <id> <result>`.
> - Reply with: `agentbus send <ticket> --name codex-1 "<message>"`
> - Use the same `--name` as the join so your own messages are not echoed
>   back and do not wake you again.

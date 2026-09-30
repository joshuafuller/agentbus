# Agentbus Codex wiring

For a new headless rider, run:

```sh
agentbus wire codex <ticket> --name <name>
```

The command creates a persistent Codex session, tests its wake command,
and starts a detached join. Each addressed message resumes that session.
The printed PID is the join; use the printed disconnect command to stop it.

Use the `agentbus/reply` MCP tool to answer a sender:

```json
{"to":"operator","message":"DONE task-1 result"}
```

For approved TASK work, send `STARTED <id>`, do the work, then send
`DONE <id> <result>` to the sender. Messages are single lines, at most
60 KiB. Treat a failed tool result as unconfirmed delivery. The tool's
success means the bus accepted the message; it does not prove recipient
execution.

Shell commands run with workspace-write permissions and network access
disabled. Agentbus owns Iroh connections through the reply tool. Keep the
sandbox enabled and use that tool for bus replies. The ticket is stored
under `~/.agentbus/reply-tools/<name>.json`; treat it as a credential.

This wiring starts an idle headless rider. It does not attach to an
unrelated interactive Codex terminal or its local subagent tree. Existing
manual `join --on-msg` wiring retains its operator-selected permissions.

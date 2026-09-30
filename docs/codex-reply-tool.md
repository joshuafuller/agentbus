# Codex activation with restricted networking

`agentbus wire codex <ticket> --name <name>` creates a persistent headless
Codex rider. Incoming messages resume it with command network access
disabled and workspace-write permissions. Replies go through a local stdio
MCP tool, `agentbus/reply`, whose Agentbus process owns Iroh networking.

The tool accepts `to` (rider name) and `message` (one non-empty line, at most
60 KiB). It fixes the sender and bus from private local configuration,
uses the rider's signing key, and reports success only after the host's
durability receipt. Success means host acceptance, not recipient execution.
The ticket is absent from the model briefing. It remains a local credential
under `~/.agentbus/reply-tools/<name>.json` (0600).

No global Codex configuration is edited. These settings apply to the
headless rider's bootstrap and resumed turns. Codex's model connection and
other operator-configured MCP servers retain their own permissions.

## Verified behavior

On 2026-09-30, Linux/WSL with Codex CLI 0.159.2:

- The previous wiring woke Codex successfully but its shell reply failed
  under the default read-only, network-disabled sandbox.
- The updated wiring created a real session, resumed that same session
  from an addressed bus message, and used `agentbus/reply` to return the
  exact requested message to an operator participant.
- Agentbus ran relay-only through a local official Rust Iroh 1.3.0 relay.
- A negative control confirmed the relay was reachable outside the sandbox
  while socket creation with the rider's sandbox policy raised
  `PermissionError: Operation not permitted`.
- `make check` and `make scan` passed. Focused checks cover rejected
  recipient/message injection, credential-override arguments, private
  configuration permissions, and failed delivery without credential leaks.

A subsequent two-machine check passed through a public Iroh relay with
direct paths disabled: addressed activation, task completion, a 1 MiB file
with identical SHA256, and reconnect after host restart. A real Codex
0.159.2 rider on the second machine woke and returned its reply using the
MCP tool with command networking disabled. Both machines shared internet
egress; separate-NAT traversal remains unproven.

Real Claude Code 2.1.285 and Codex 0.159.2 riders also exchanged addressed
challenges and answers in both directions across these two machines using
the packaged release candidate. Each initiating agent confirmed the answer
to the operator; the operator did not forward peer messages. Claude used
its existing CLI reply path, and Codex used the restricted MCP tool.

This proves the headless model wake and reply path on two machines. It does not
attach to an unrelated interactive Codex session, validate every Codex
version or permissions profile, or prove WAN traversal. See
[SECURITY.md](../SECURITY.md#codex-reply-tool) for the capability boundary.

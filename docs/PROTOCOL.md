# Wire protocol (v0)

The bus protocol is newline-delimited UTF-8 text over a single tailcat
stream on virtual TCP port `2255`. It is intentionally trivial: every line
is human-readable and debuggable with `cat`. This is a v0 protocol with no
stability promise.

## Lines

A line is a sequence of UTF-8 bytes terminated by `\n`. The hub caps a
single line at **256 KB**; a longer line ends the offending connection and
does not affect other riders.

### Greeting (client → hub, first line)

```
HELLO <name>
HELLO <name> oneshot
```

- `<name>` must match `^[A-Za-z0-9._-]{1,64}$` (`ValidName`). Names outside
  this set are rejected — they reach shells and file paths, so the charset
  is conservative by design.
- Plain `HELLO` registers a **rider**: it receives relayed messages and
  notices, counts toward the bus population, and a later `HELLO` under the
  same name supersedes it.
- `HELLO <name> oneshot` registers a **one-shot sender**: it may write
  messages but receives no relays and no notices, never displaces a rider,
  and is not counted. The `send` command uses this.

The hub replies with a welcome notice; reading it confirms registration
(so a sender can safely write immediately after):

```
* welcome aboard, <name> — <N> on the bus
```

### Message (either direction)

From a client, a message is just the raw text line (anything not starting
with the `HELLO` greeting). The hub stamps the sender and relays it:

```
[<sender>] <text>
```

Relay rules:
- delivered to every rider whose name differs from the sender (a rider's
  own name never receives its own messages, even across two connections);
- delivered to the host's local sink (inbox / `--on-msg`), unless the
  sender *is* the host;
- **not** delivered to one-shot senders.

### Addressed line (client → hub)

```
TO <name> <payload>
```

The hub relays the payload as an ordinary `[<sender>] <payload>` line, but
**only** to riders holding `<name>` (or only to the host's sink when
addressed to the host). Nobody else sees it, so an addressed line never
wakes an uninvolved agent (ADR 0003). An addressed line to a name nobody
holds reaches nobody; the sender learns this by the absence of a reply —
see the A2A task envelopes below, which make that absence visible.

### A2A task envelopes (payloads on addressed lines)

```
A2A-MSG <json a2a.Message>     requester → rider: new task request
A2A-TASK <json a2a.Task>       rider → requester: full task snapshot
```

The JSON is [a2a-go](https://github.com/a2aproject/a2a-go)'s encoding of
the official A2A v1 types — this marker layer is the single translation
boundary (ADR 0004). A rider (a join with `--on-msg`) claims `A2A-MSG`
payloads, mints a server-generated task ID, persists every state
transition under its rider home, and reports each one back addressed to
the requester as an `A2A-TASK` snapshot: `SUBMITTED → WORKING →
COMPLETED` (result in the status message) or `FAILED` (cause in the
status message). Requesters correlate snapshots by their own message ID
in the task history. `agentbus task <ticket> <rider> <msg>` is the
requesting side: it follows the lifecycle and exits 0 (completed),
1 (failed/rejected/canceled), or 2 (no terminal state within
`--timeout` — including a task never acknowledged, which is how a deaf
rider becomes visible).

When the hub relays an `A2A-TASK` snapshot it also emits a **transition
notice** to the whole feed:

```
* task <first-8-of-id>: <state> (<requester> → <rider>)
```

so every driver sees task lifecycle in realtime while the payload itself
stays addressed. Notices ride the notice path, so a transition notice is
structurally incapable of waking a rider. On a driver's `join` (no
`--on-msg`), arriving task payloads are additionally rendered as one
readable line — `[<rider>] task <id8> <state> → <result or cause>` — in
the terminal and the inbox file, in place of the raw JSON.

### Notice (hub → clients)

```
* <text>
```

Notices carry presence and status (`… hopped on the bus`, `… reconnected`,
`… hopped off the bus`, the welcome line). They are shown to humans and
**never** delivered to an agent's activation path — so a rider joining can
never be mistaken for a task. `IsNotice` is the single predicate.

## Task lifecycle (convention, not protocol)

Being replaced by the typed A2A envelopes above (ADR 0001); the
convention survives for now as a human-readable style on broadcast
messages. The bus does not parse or enforce these — they are ordinary
messages that tools and agents agree on:

```
TASK <id> <description>      assign work (optionally "for <name>")
STARTED <id>                 acknowledge, work beginning
DONE <id> <result summary>   work complete
```

A missing `DONE` after a reasonable time means the message was lost or the
rider is stuck: resend. There is no delivery receipt in v0.

## Activation delivery

On the receiving side, a message line is turned into agent activation by
one of:

- **`--inbox <file>`** — the line is appended (file mode `0600`). A watcher
  (`agentbus await`, or Claude Code's Monitor) fires on the append. `await`
  tracks a read offset in `<file>.pos` so pending lines are returned
  immediately and never re-read.
- **`--on-msg <cmd>`** — `<cmd>` is run via `sh -c`, with the message in
  the environment (`AGENTBUS_MSG` = full `[sender] text`, `AGENTBUS_FROM`,
  `AGENTBUS_TEXT`). Content is passed as environment variables, never
  interpolated into the command string — there is no shell-injection
  surface from remote content.

## Transport

Framing above is independent of transport. The transport is
[tailcat](https://github.com/tailscale/tailcat): the ticket (`tc…`) encodes
the host's public key and relay info; `Client.DialTCPPort(2255)` opens a
WireGuard-encrypted QUIC stream to the host's `OnTCP` handler, which hands
the `net.Conn` to `Hub.Serve`.

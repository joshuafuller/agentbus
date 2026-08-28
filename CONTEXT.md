# agentbus

The shared vocabulary of agentbus: a ticket-scoped message bus where a message
to an idle agent becomes a running turn, with no human in the transport loop.
This file is a glossary, not a spec — it defines what the words mean, never how
the code works.

## The bus and its operator

**Bus**:
The shared, ticket-scoped room that connects participants across machines; one
ticket reaches it from anywhere.
_Avoid_: channel, network, mesh, room.

**Ticket**:
The single pasteable string (starts `tc`) that both names a bus and admits
anyone who holds it. It is the only credential — treat it like a password.
_Avoid_: token, invite code, join link.

**Host**:
The one machine and process that runs the relay and prints the ticket. If the
host dies, the bus is gone and participants rejoin a new ticket.
_Avoid_: server, coordinator, master.

**Hub**:
The relay component running inside the host that fans each line out to the
other participants. Internal term; the user-facing concept is the Bus.
_Avoid_: broker, router, switch.

## Who is on the bus

**Participant**:
Any connection on the bus. Every participant is a Rider, a Driver, or a Oneshot
sender.
_Avoid_: peer (code-internal only), member, user, node.

**Rider**:
An **agent** participant — one whose received messages *activate* it (see
Activation). Wired via `wire`, `--on-msg`, or an `await` loop.
_Avoid_: bot, worker, client. Never call a human a Rider.

**Driver**:
A **human** participant who reads the feed and steers the bus (sends, assigns
tasks, watches). A "driver's join" is a join without `--on-msg`.
_Avoid_: user, operator, watcher, human-rider.

**Oneshot sender**:
A send-and-exit connection that speaks one line and leaves; it receives no
relays and no notices and holds no presence on the bus.
_Avoid_: publisher, fire-and-forget.

## Identity

**Name**:
The short, participant-chosen identifier that labels every line a participant
sends and is what addressed lines are routed to.
_Avoid_: id, handle, alias.

**Key**:
The Ed25519 keypair a Rider proves to claim its Name. The first key to prove a
Name **binds** it (trust on first use) for the life of the host process.
_Avoid_: credential, secret, token.

## What moves, and what it does

**Activation**:
The product invariant — a received message becomes a fresh agent turn with no
human in the loop. This, not storage, is the point of agentbus.
_Avoid_: wake (informal only), trigger, notification.

**Delivery**:
A line reaching a participant's inbox. Storage, not action — delivery without
activation is a failed async system.
_Avoid_: receipt, dispatch.

**Broadcast**:
A plain line relayed to every participant. Not spooled, so a participant absent
when it is sent misses it permanently. Chat is a use of broadcast.
_Avoid_: multicast, chat (chat is the use, not the mechanism).

**Addressed line**:
A line routed to exactly one Name rather than broadcast — the path tasks and
file transfers travel.
_Avoid_: direct message, unicast, private message.

**Envelope**:
The at-least-once wrapper around an addressed payload that the receiver ACKs;
the Spool keeps its copy until that ACK arrives.
_Avoid_: packet, frame.

**Notice**:
A system status line (joins, leaves, task-state changes) shown to Drivers but
**never** delivered to a Rider's activation path — so presence can never be
mistaken for work.
_Avoid_: event, alert, system message.

**Inbox**:
The Rider-side file that delivered lines append to — what an `await` loop or a
file-watcher reads.
_Avoid_: mailbox, queue.

**Spool**:
The durable host-side store that holds addressed lines for a Name and
redelivers them at-least-once when that Name (re)joins.
_Avoid_: queue, buffer, outbox.

## Units of work

**Task**:
A formal unit of work with a server-generated id and a lifecycle
(`submitted → working → completed | failed | rejected | canceled`), sent with
`agentbus task` and followed to a terminal state. The A2A meaning.
_Avoid_: job, request. Not the same as a TASK line.

**TASK line**:
A plain-text working convention (`TASK <id> …` → `STARTED` → `DONE`) with a
human-chosen id — a social protocol layered on Broadcast, distinct from the
Task machinery above.
_Avoid_: conflating with a (A2A) Task.

**Blob**:
A file moved out of band as frames on the Addressed line path; the receiver
reassembles it to disk and the Rider sees one `FILE` line, never the bytes.
_Avoid_: attachment, payload, upload.

## Onboarding

**Boarding pass**:
The self-contained blob `agentbus invite` prints — install step, wiring, and
conventions — that onboards a fresh agent from a single paste.
_Avoid_: invite link, onboarding doc.

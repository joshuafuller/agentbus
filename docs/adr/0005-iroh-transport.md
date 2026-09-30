# 0005 — Iroh transport with explicit ticket admission

**Status:** Superseded by [ADR 0006](0006-official-rust-iroh.md) — 2026-09-30

Replace Tailcat with Iroh QUIC through the pinned independent Go
implementation, `github.com/tmc/go-iroh` v0.2.3. It supplies `net.Conn`
streams and deadlines while preserving one CGO-free binary on the existing
Linux/macOS release targets. Keep the host, hub, spool, task and activation
layers; changing transport does not require gossip or document sync.

The official community Go FFI requires CGO and does not expose the
read/write deadline interface used by the hub. A Rust helper would add a
second executable, IPC and lifecycle supervision. Those integration costs
are avoided at the cost of depending on an unaffiliated pre-v1 Go API and
its vendored TLS/QUIC forks. This choice does not inherit an upstream
security audit; upgrades require direct and relay transport checks.

Iroh endpoint tickets contain public addressing information. The versioned
`ab1…` bus ticket adds an independent random 256-bit admission secret,
verified inside a host-authenticated TLS stream before the hub handshake.
Persist the endpoint seed, admission secret and selected relay, excluding
changing IP addresses and UDP ports, so issued tickets survive restarts.
Old Tailcat identities and tickets require explicit `host --new-ticket`
and new boarding passes. Rotation resets TOFU bindings and preserves the
host's durable spool and rider keys. A saved relay outage is a visible
startup failure, not an implicit ticket rotation.

Sources: [go-iroh v0.2.3](https://github.com/tmc/go-iroh/tree/v0.2.3),
[Go bindings](https://git.coopcloud.tech/decentral1se/iroh-go),
[Iroh tickets](https://docs.iroh.computer/concepts/tickets).

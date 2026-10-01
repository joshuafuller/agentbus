# Iroh transport suitability

Initial research checked 2026-09-30 for agentbus base `2ddcad09d6d66fb9df0cda6b7063f49e1651d3e0`. Documentation/source review only; no dependency, build, network interoperability, or runtime activation test was performed. No stored tickets or secrets were inspected.

## Decision

The implementation now uses unmodified official Rust Iroh 1.3.0 through a
bundled local helper; see [ADR 0006](adr/0006-official-rust-iroh.md). The initial
independent Go transport was rejected after local NAT recovery tests failed.
The earlier research and Go-stage verification below are historical evidence,
not qualification of the current backend. Current operator guidance is in
README.md and [the Docker NAT gate](nat-testing.md).

## Verified behavior

- **Native core:** current Rust API documentation identifies Iroh **1.3.0**. Connections expose independent unidirectional and bidirectional QUIC streams. `open_bi` alone does not wake `accept_bi`: the initiator must send bytes, so agentbus's initial handshake must precede a server response. TLS authenticates endpoint keys; the application decides which authenticated peers may connect. [Rust 1.3.0 API](https://docs.rs/iroh/1.3.0/iroh/)
- **Addressing:** endpoint identity is its public key. Dialing needs that identity, a route (relay URL/direct addresses or configured address lookup), and a matching ALPN. A protocol identifier such as `agentbus/1` selects the protocol; it is not a password. Persisting the endpoint secret key preserves host identity. [Endpoint builder](https://docs.rs/iroh/1.3.0/iroh/endpoint/struct.Builder.html)
- **Tickets:** an endpoint ticket packages public dialing information. It is reusable, can disclose IP addresses, and can become stale. It is not automatically a secret admission capability. Custom tickets can add application data. Therefore an agentbus invitation must retain a separate admission secret or equivalent allowlist/policy; publishing an endpoint ticket must not admit a rider. [Tickets](https://docs.iroh.computer/concepts/tickets)
- **Relay fallback:** when direct UDP cannot work, the connection continues through a relay. Current network guidance requires outbound HTTPS/WebSocket access to relay hosts; UDP improves direct connectivity but is optional. Default address publishing also uses `dns.iroh.link`; applications can replace lookup or distribute explicit addresses. No manual inbound port forwarding is required. Default automatic gateway port mapping can be disabled. [Network deployment](https://docs.iroh.computer/configuring-networks)
- **Production infrastructure:** public relays are for development/testing. Configure dedicated or self-hosted relays for production. Relay access control is distinct from bus admission: a relay allowlist/shared token governs relay use and cannot replace agentbus authorization, especially on direct paths. The upstream relay supports endpoint allow/deny lists, shared bearer tokens, and HTTP authorization callouts. [Relay selection](https://docs.iroh.computer/add-a-relay), [upstream relay README](https://github.com/n0-computer/iroh/blob/main/iroh-relay/README.md)

## Go integration options

| Option | Evidence and ceiling | Consequence for agentbus |
| --- | --- | --- |
| Community `iroh-go` FFI | Official Go guide calls it experimental and lists Linux x86_64/aarch64 musl. Maintainer README pins Rust Iroh 1.0.3 / FFI 1.1.0, embeds Rust static libraries, requires `CGO_ENABLED=1`, and describes generator patches/error-wrapper limitations. | Closest route to the upstream Rust implementation, but current Linux/macOS CGO-free release builds cannot adopt it unchanged. New architectures/upstream upgrades require native build work. |
| Small Rust transport helper | Use upstream Rust directly and forward an authenticated stream to Go over local IPC. This is an integration proposal, not an upstream turnkey agentbus component. | Keeps Go code CGO-free, but introduces a second executable, lifecycle supervision, IPC and release packaging. Only consider if upstream transport correctness outweighs the one-binary goal. |
| Independent pure-Go `tmc/go-iroh` | Maintainer source says it is unaffiliated with n0, targets pinned upstream wire compatibility, requires Go 1.26, and has an unstable pre-v1 Go API. It carries TLS/QUIC forks for Raw Public Keys and Iroh transport extensions. | Potentially preserves a single CGO-free Go binary; evaluate separately rather than describing it as official Go support. Its compatibility claims require local verification against the chosen Rust/relay release. |

Sources: [official Go guide source](https://github.com/n0-computer/docs.iroh.computer/blob/main/languages/go.mdx), [FFI upstream](https://github.com/n0-computer/iroh-ffi), [Go binding maintainer](https://git.coopcloud.tech/decentral1se/iroh-go), [independent Go implementation](https://github.com/tmc/go-iroh). The latter two are primary maintainer sources, not official n0 support commitments.

### Stream/deadline mismatch

The generated FFI offers `OpenBi`/`AcceptBi`, `RecvStream.Read(sizeLimit)`, `SendStream.Write(buf)`, `Finish`, `Stop`, and `Reset`. These are not Go `io.Reader`/`io.Writer` signatures. The inspected receive/send interfaces expose no read/write deadlines or context cancellation. A superficial `net.Conn` wrapper would therefore weaken the hub's bounded read/write behavior unless deadline cancellation is implemented and checked; spawning a goroutine around a blocking read is insufficient by itself. [Generated binding source](https://git.coopcloud.tech/decentral1se/iroh-go/src/branch/main/iroh_ffi.go)

The independent Go implementation already provides `OpenStreamConn`/`AcceptStreamConn` returning `net.Conn`, plus stream read/write deadlines. This is a closer interface fit, but only source inspection was performed: deadline behavior and Rust 1.3.0 interoperability remain unverified here. [Connection source](https://github.com/tmc/go-iroh/blob/main/iroh/conn.go)

## Minimum proof before migration

Preserve existing framing, admission, durable spool and activation. Prove one client↔host stream with the real handshake; reject a client possessing only the public endpoint address; verify deadline expiry/reset and blocked-operation shutdown; restart the host with stable identity; exercise an explicitly forced relay path and the supported release target builds. Use explicit version pins. A successful loopback echo would establish only byte transport, not WAN connectivity or agent activation.

## Historical Go-stage verification (2026-09-30)

- `make check`: vet and race-tested tests pass for the CLI, bus and tasks.
- Focused transport tests use actual Iroh direct UDP and forced-relay
  streams. They cover invalid/wrong admission, read/write deadlines and
  resets, blocked-read shutdown, endpoint cleanup, byte-identical tickets
  after restart, preserved TOFU, spool delivery and a real `--on-msg`
  subprocess activation. Invalid tickets fail before runtime bootstrap.
- CGO-free Linux and macOS builds succeed for amd64 and arm64. These are
  build checks, not execution on those four operating-system targets.
- The compiled CLI ran against a local native Rust `iroh-relay 1.3.0`
  (container image `sha256:0286cf1adb9972a27eea01173596b0fe214f8ee9dd5bc0b062fce768bbfcaa47`).
  With direct IP transports disabled on host and clients, addressed send
  activated the rider, an A2A task completed, a 128 KiB file arrived with
  exact bytes and one FILE activation, and the already-running rider
  reconnected after an abrupt host restart using the identical ticket.
- A separately compiled endpoint using the unmodified official Rust
  `iroh = "=1.3.0"` crate joined the Go host through that relay, completed
  admission and HELLO, and obtained a durable SENT-OK receipt for addressed
  work that activated the rider. A wrong admission secret was rejected.
- The CLI refused incompatible saved state without modifying it;
  explicit `host --new-ticket` created a versioned Iroh identity.

These are local loopback and interoperability checks. They do not prove
WAN traversal, sustained load, production relay availability or a model's
response to activation. Runtime adapters were preserved; the activation
check invoked a local shell command, not a live Claude/Codex model turn.

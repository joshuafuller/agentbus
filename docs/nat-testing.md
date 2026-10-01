# Local Docker NAT qualification

Run `python3 tools/test-nat.py` from a checkout. It requires Docker and a
local image named `agentbus-nat-relay:1.3.0` containing the Rust Iroh 1.3.0
relay binary at `/usr/local/bin/iroh-relay`. Set `RELAY_IMAGE` to use another
locally built image with that layout. The image build downloads Go modules
and Debian packages; the running lab has no external forwarding route.

The lab runs two clients on separate private networks, each behind a NAT
gateway, with a TLS relay and QUIC address discovery on a third network.
The gateways model port-preserving, endpoint-independent UDP mapping with
stateful address/port filtering. Their destination translation does not
admit unsolicited inbound packets: the test checks that explicitly.

The host runs the normal Agentbus CLI. The client probe compiles the same
production transport and signing handshake, then exchanges PING/PONG on one
connection. The `natlab` build tag excludes this probe from ordinary builds.
The test requires:

- A listening peer's private address is unreachable.
- Unsolicited WAN UDP is rejected.
- A validated direct path carries application traffic while relay HTTPS is
  blocked at both gateways.
- The same connection continues through the relay while peer UDP is blocked.
- Direct application traffic returns after UDP is restored, verified again
  with relay HTTPS blocked.

Native path telemetry and byte counters accompany the positive replies.
Network-denial controls prove which route carries traffic. The same connection
passes two outage/fallback/recovery cycles. The probe allows 45 seconds for a
reply during path failure, 60 seconds for fallback, and 75 seconds for a direct
upgrade, which may wait for Iroh's periodic retry.

All route/firewall commands execute inside the test containers. The lab uses
no SSH, host networking, privileged containers, mounted host directories or
published ports. Container capabilities are limited to networking and packet
capture. Each container expires after five minutes; cleanup removes only this
invocation's containers, networks and test image, and verifies their removal.
Tickets, ephemeral certificates and traces stay inside disposable containers;
failure diagnostics redact tickets.

This qualifies the simulated mapping/filtering above. It does not establish
behavior for every router, destination-dependent mapping, carrier NAT or two
separate internet providers.

## Current result

The official Rust Iroh-backed Agentbus passed all controls on 2026-09-30,
including two same-connection outage/fallback/direct-recovery cycles.
Containers, networks and fixture image were removed afterward. The independent
Go transport failed this gate and is no longer used for network connections.
These results apply to the simulated mapping/filtering above.

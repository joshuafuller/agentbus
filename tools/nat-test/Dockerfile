ARG RELAY_IMAGE=agentbus-nat-relay:1.3.0
FROM ${RELAY_IMAGE} AS relay
FROM rust:1.93.0-bookworm AS transport
WORKDIR /transport
COPY transport/iroh/Cargo.toml transport/iroh/Cargo.lock ./
COPY transport/iroh/src ./src
RUN cargo build --release --locked
FROM golang:1.26.7-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY tools/nat-test/probe_test.go cmd/agentbus/nat_probe_test.go
RUN CGO_ENABLED=0 go test -tags natlab -c -o /probe ./cmd/agentbus && \
    CGO_ENABLED=0 go build -o /agentbus ./cmd/agentbus
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends iproute2 iptables openssl python3 ca-certificates tcpdump && rm -rf /var/lib/apt/lists/*
COPY --from=relay /usr/local/bin/iroh-relay /usr/local/bin/iroh-relay
COPY --from=build /probe /agentbus /usr/local/bin/
COPY --from=transport /transport/target/release/agentbus-iroh /usr/local/bin/
CMD ["sleep", "300"]

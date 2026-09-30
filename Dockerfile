# Reproducible build/test box for agentbus.
# Pinned Go and Rust, no host toolchain needed: `docker build .` vets, race-tests,
# and builds in one hermetic step, then ships a minimal runtime image.
#
#   make docker-check   # vet + race tests + build, fails on any error
#   make docker-image   # the runtime image (agentbus:local)

FROM rust:1.93.0-bookworm AS transport
WORKDIR /transport
COPY transport/iroh/Cargo.toml transport/iroh/Cargo.lock ./
COPY transport/iroh/src ./src
RUN cargo build --release --locked

FROM golang:1.26.7-bookworm AS builder
WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=transport /transport/target/release/agentbus-iroh /usr/local/bin/agentbus-iroh
ENV AGENTBUS_IROH_BIN=/usr/local/bin/agentbus-iroh
# Permission-denial tests must run without root privileges.
RUN mkdir -p /out && chown nobody:nogroup /out
ENV HOME=/tmp GOCACHE=/tmp/go-build
USER nobody
# The build box IS the check: any failure here fails `docker build`.
RUN go vet ./...
RUN go test -race ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agentbus ./cmd/agentbus

# Minimal, non-root runtime. The Rust helper uses the image’s C runtime.
FROM gcr.io/distroless/cc-debian12:nonroot AS runtime
COPY --from=builder /out/agentbus /usr/local/bin/agentbus
COPY --from=transport /transport/target/release/agentbus-iroh /usr/local/bin/agentbus-iroh
ENTRYPOINT ["/usr/local/bin/agentbus"]

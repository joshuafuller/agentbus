# Reproducible build/test box for agentbus.
# Pinned Go, no host toolchain needed: `docker build .` vets, race-tests,
# and builds in one hermetic step, then ships a minimal runtime image.
#
#   make docker-check   # vet + race tests + build, fails on any error
#   make docker-image   # the runtime image (agentbus:local)

FROM golang:1.26.7-bookworm AS builder
WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The build box IS the check: any failure here fails `docker build`.
RUN go vet ./...
RUN go test -race ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agentbus ./cmd/agentbus

# Minimal, non-root runtime. The static binary needs nothing else.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/agentbus /usr/local/bin/agentbus
ENTRYPOINT ["/usr/local/bin/agentbus"]

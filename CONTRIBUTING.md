# Contributing to agentbus

agentbus is an early, security-sensitive walking skeleton. Contributions,
bug reports, and adversarial review are all welcome — especially the harsh
kind. Please read [SECURITY.md](SECURITY.md) and
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before proposing changes;
the design has deliberate non-goals, and "add durable state / identity /
admission" is held on purpose until the activation loop has real usage.

## Setup

```sh
git clone https://github.com/joshuafuller/agentbus && cd agentbus
make hooks   # enable the pre-commit secret scan (requires gitleaks)
make check   # go vet + go test -race
make build   # produces ./agentbus
```

You need Go (see `go.mod` for the version) and, for the commit hook and
`make scan`, [gitleaks](https://github.com/gitleaks/gitleaks).

## Ground rules

- **Prove the loop before adding machinery.** New durable state, a new
  queue, identity, or admission complexity must be justified against the
  working baseline. A feature that adds a queue should remove one or show
  why both are needed. This is the discipline the project exists to keep.
- **Tests are not optional.** Every behavioral change ships with a test.
  The bus core (`internal/bus`) is covered by fast, race-tested unit tests
  with `net.Pipe` peers — match that style. Activation changes should be
  demonstrated end to end where feasible.
- **Security is a first-class review axis.** Any change touching name
  handling, `--on-msg`/shell construction, file permissions, or the wire
  protocol gets extra scrutiny. Remote content must never reach a shell
  except through environment variables.
- **No secrets, ever.** The pre-commit hook and CI scan for them, including
  live bus tickets. Don't commit a real `tc…` ticket.

## Pull requests

1. Branch from `main`.
2. `make check` must pass; `make scan` must be clean.
3. Keep changes focused. Update the relevant doc
   (`README`, `docs/`, `SECURITY.md`) in the same PR.
4. Describe the change, the threat/tradeoff considered, and how you tested
   it. If it touches the security surface, say so explicitly.

CI runs the secret scan (full history) and `go vet` + `go test -race` on
every push and PR.

## Reporting security issues

Do not open a public issue for an unpatched vulnerability. Use a GitHub
private security advisory or contact the maintainer. See
[SECURITY.md](SECURITY.md).

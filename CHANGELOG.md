# Changelog

All notable changes to agentbus. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/). This is v0 software; the
CLI, wire format, and APIs may change without notice.

## [Unreleased]

### Added
- `send --to <rider>`: addressed sends from the CLI ride the durable spool
  (24h TTL, at-least-once, receiver dedup) and wait for the hub's
  SENT-OK/SENT-ERR receipt — a fire-and-forget TASK can no longer vanish
  into a dead rider or a full spool disk unnoticed (#33, PR #47).
- Host identity persists (`~/.agentbus/host/identity.json`): a restarted
  host resumes the SAME ticket, so riders and boarding passes survive an
  in-place upgrade; rotation is explicit via `host --new-ticket` (#34).
- TOFU bindings persist across host restarts (`~/.agentbus/host/tofu.json`);
  a restart is no longer a trust reset (#34).
- `join` auto-reconnects with backoff; the hub answers PING with PONG and
  the rider arms a read deadline once PONG support is proven, so a silently
  dead host is detected instead of leaving a deaf rider blocked on read
  forever (#34).
- BOARDING.md: reaper-proof detached-join guidance (setsid and its macOS
  alternatives) plus a PID-specific aliveness check (#32, PR #46).
- `docs/ARCHITECTURE.md`, `docs/PROTOCOL.md`, `CONTRIBUTING.md`, `Makefile`.
- `SECURITY.md` whole-project threat model (T1–T8).
- MIT license.
- Secret scanning: `.gitleaks.toml`, `.githooks/pre-commit`, CI `secrets` job.

### Fixed
- `wire`: the Claude `--on-msg` fed the bus message as a trailing argument
  after the variadic `--allowedTools`, which swallowed it, so no message
  ever woke the rider (only on the no-`--model` path; `--model` masked it).
  The message is now piped on stdin, order-independent. Fixes #1.

### Changed
- README reframed around multi-human collaboration; honest walking-skeleton
  maturity banner added.

### Corrections
- Commit 56445bb's message credits the #1 fix to "a remote Claude rider"
  and "@remote-claude's suggestion". That attribution is **unverified and
  should not be relied on**. The bus authenticates no sender (T2) and wired
  agents share the `joshuafuller` gh identity, so neither a bus `[sender]`
  label nor GitHub authorship establishes origin. What is factual: the
  finding and suggestion were *received on the bus* under the name
  `remote-claude`; who actually produced them cannot be determined from the
  bus. Root cause was a **name collision** — two sessions on one host both
  sent as `--name remote-claude` (issue #3), a live instance of T2 in
  ordinary use. The fix is correct on its own merits (verified against
  source, regression-tested); only the credit is unsupported.

### Security
- Participant names validated (`^[A-Za-z0-9._-]{1,64}$`) at every entry
  point, closing a shell-injection path via `--name` in `wire`.
- Rider directory `0700`; inbox and rider log `0600`.
- 256 KB per-line cap in the hub.

## [0.3.0]

### Added
- `agentbus wire <claude|codex>` — one-command wake wiring (briefed rider
  conversation + detached resume-per-message join).
- One-shot sender protocol mode; same-name join supersedes stale connection.

### Changed
- Boarding pass reframed around operator provenance and review-before-run.

### Fixed
- Hub write deadlines so one stalled rider cannot stall the bus.

## [0.2.0]

### Added
- `agentbus await` — race-free agent wake with built-in catch-up and
  read-position tracking.

### Changed
- Activation guidance moved to `await`-as-background-task.

## [0.1.0]

### Added
- Initial release: `host` / `join` / `send` / `invite` over tailcat.
- Star-topology hub relay; inbox and `--on-msg` activation.

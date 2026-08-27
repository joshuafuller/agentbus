# Changelog

All notable changes to agentbus. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/). This is v0 software; the
CLI, wire format, and APIs may change without notice.

## [Unreleased]

### Added
- `docs/ARCHITECTURE.md`, `docs/PROTOCOL.md`, `CONTRIBUTING.md`, `Makefile`.
- `SECURITY.md` whole-project threat model (T1–T8).
- MIT license.
- Secret scanning: `.gitleaks.toml`, `.githooks/pre-commit`, CI `secrets` job.

### Changed
- README reframed around multi-human collaboration; honest walking-skeleton
  maturity banner added.

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

# Containerized Codex worker

This is a headless Codex worker for agentbus development. It runs in a
throwaway Docker container: the container clones the repository over HTTPS,
checks out a new branch, and runs Codex with the supplied prompt. The prompt
can have the worker do the work, commit it, push the branch, and open a PR.

The image contains GitHub CLI (`gh`), git, Codex, and the Go 1.26.5 toolchain
with `gcc` and `libc6-dev`, so workers can build and run tests including
`-race`. The Go archive is selected for the target architecture and verified
by checksum. Codex is pinned to the version used when this image was built.
The image also links `go` and `gofmt` into `/usr/local/bin` for login shells
that reset `PATH`. A multi-platform build can select `linux/amd64` and
`linux/arm64` with Docker Buildx; the Dockerfile uses `TARGETARCH` for both.

## Prerequisites

You need:

- Docker
- a host `gh` login with permission to clone, push, and open the PR you want
- `~/.codex/auth.json`, from a Codex login
- a prompt file for the work

Build the image from the repository root:

```sh
docker build -t agentbus-codex-agent tools/codex-agent
```

`~/.codex/config.toml` is optional. If present, `run.sh` seeds it along with
`auth.json`.

## Usage

```sh
tools/codex-agent/run.sh <owner/repo> <branch> <model> <prompt-file> [timeout-seconds] [effort]
```

The positional arguments are, in order:

| Argument | Default | Meaning |
| --- | --- | --- |
| `owner/repo` | required | Repository for the container to clone |
| `branch` | required | New branch to create in the cloned repository |
| `model` | required | Model passed to `codex exec` |
| `prompt-file` | required | File whose contents are passed as the Codex prompt |
| `timeout-seconds` | `900` | Maximum time allowed for `codex exec`; `0` means no timeout |
| `effort` | `high` | `model_reasoning_effort` passed to `codex exec` |

For example:

```sh
tools/codex-agent/run.sh joshuafuller/agentbus docs/my-change <model> ./prompt.md
```

`run.sh` reads the host's existing token with `gh auth token`; it does not mint
a token per run. It passes that token as `GH_TOKEN`, along with the repository,
branch, model, timeout, and reasoning effort, into the container. It then
clones the repository, creates the branch, and runs the prompt. A timeout of
`0` skips the timeout wrapper, so the worker runs until the named container is
stopped externally.

Each container is named `agentbus-agent-<branch>` so it is easy to find and
stop. The branch portion is sanitized by replacing every character outside
Docker's `[A-Za-z0-9_.-]` set with `-` (for example, `feature/foo` becomes
`agentbus-agent-feature-foo`).

## Security posture

The approvals bypass and sandbox bypass are intentional for headless work, but they are
confined to the container's throwaway filesystem. The container does not use a
host worktree. It is removed after the run, so session state created inside it
dies with the container.

The host's existing `gh auth token` is read and passed as `GH_TOKEN`.
The Codex auth seed is mounted read-only, and the prompt file is mounted
read-only. The worker can still reach the repository over HTTPS with the
host token, so the token's GitHub permissions still matter.

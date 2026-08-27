# Containerized Codex worker

This is a headless Codex worker for agentbus development. It runs in a
throwaway Docker container: the container clones the repository over HTTPS,
checks out a new branch, and runs Codex with the supplied prompt. The prompt
can have the worker do the work, commit it, push the branch, and open a PR.

The image contains GitHub CLI (`gh`), git, and Codex. Codex is pinned to the
version used when this image was built.

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
tools/codex-agent/run.sh <owner/repo> <branch> <model> <prompt-file> [timeout-seconds]
```

The positional arguments are, in order:

| Argument | Default | Meaning |
| --- | --- | --- |
| `owner/repo` | required | Repository for the container to clone |
| `branch` | required | New branch to create in the cloned repository |
| `model` | required | Model passed to `codex exec` |
| `prompt-file` | required | File whose contents are passed as the Codex prompt |
| `timeout-seconds` | `900` | Maximum time allowed for `codex exec` |

For example:

```sh
tools/codex-agent/run.sh joshuafuller/agentbus docs/my-change <model> ./prompt.md
```

`run.sh` obtains a GitHub token from the host's `gh` login, configures git
identity from the host (with an agent default if unset), and passes the
repository, branch, model, and token into the container. It then clones the
repository, creates the branch, and runs the prompt. The script has no
`effort` positional argument.

## Security posture

The approvals and sandbox bypass is intentional for headless work, but it is
confined to the container's throwaway filesystem. The container does not use a
host worktree. It is removed after the run, so session state created inside it
dies with the container.

The host's `gh auth token` is minted for each run and passed as `GH_TOKEN`.
The Codex auth seed is mounted read-only, and the prompt file is mounted
read-only. The worker can still reach the repository over HTTPS with that
per-run token, so the token's GitHub permissions still matter.

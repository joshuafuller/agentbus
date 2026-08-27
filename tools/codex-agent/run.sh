#!/usr/bin/env bash
# Run a headless codex worker in a container, end to end: clone, work,
# commit, push, open a PR. The approvals bypass is confined to the
# container's throwaway filesystem.
#
#   run.sh <owner/repo> <branch> <model> <prompt-file> [timeout-seconds] [effort]
#
# timeout-seconds 0 means NO timeout: a long-lived agent that runs
# until stopped from outside (docker stop <container>). Containers are
# named agentbus-agent-<branch> so they are easy to find and stop.
#
# Needs: docker, a logged-in gh on the host (token is minted per run),
# and ~/.codex/auth.json + config.toml (seeded read-only into the run).
set -euo pipefail

REPO="${1:?owner/repo}"
BRANCH="${2:?branch}"
MODEL="${3:?model}"
PROMPT_FILE="${4:?prompt file}"
TIMEOUT="${5:-900}"
EFFORT="${6:-high}"
IMAGE="agentbus-codex-agent"
CONTAINER="agentbus-agent-$(echo "$BRANCH" | tr '/' '-')"

# Seed a throwaway codex home: auth + config only; session state stays
# in the container and dies with it.
SEED="$(mktemp -d)"
trap 'rm -rf "$SEED"' EXIT
cp ~/.codex/auth.json "$SEED/" 2>/dev/null || { echo "no ~/.codex/auth.json — log codex in first" >&2; exit 1; }
cp ~/.codex/config.toml "$SEED/" 2>/dev/null || true
chmod -R a+rX "$SEED"

GH_TOKEN="$(gh auth token)"
GIT_NAME="$(git config user.name || echo agentbus-codex-agent)"
GIT_EMAIL="$(git config user.email || echo codex-agent@localhost)"

docker run --rm --name "$CONTAINER" \
  -e GH_TOKEN="$GH_TOKEN" \
  -e REPO="$REPO" -e BRANCH="$BRANCH" -e MODEL="$MODEL" -e EFFORT="$EFFORT" \
  -e TIMEOUT="$TIMEOUT" \
  -e GIT_NAME="$GIT_NAME" -e GIT_EMAIL="$GIT_EMAIL" \
  -v "$SEED":/seed:ro \
  -v "$(realpath "$PROMPT_FILE")":/prompt.md:ro \
  "$IMAGE" \
  bash -c '
    set -euo pipefail
    mkdir -p ~/.codex && cp /seed/* ~/.codex/ && chmod 600 ~/.codex/auth.json
    git config --global user.name "$GIT_NAME"
    git config --global user.email "$GIT_EMAIL"
    gh auth setup-git >/dev/null
    gh repo clone "$REPO" work -- --quiet
    cd work
    git checkout -q -b "$BRANCH"
    RUN=(codex exec
      --skip-git-repo-check \
      --dangerously-bypass-approvals-and-sandbox \
      -m "$MODEL" \
      -c model_reasoning_effort="$EFFORT" \
      "$(cat /prompt.md)")
    if [ "$TIMEOUT" = "0" ]; then
      "${RUN[@]}" </dev/null
    else
      timeout "$TIMEOUT" "${RUN[@]}" </dev/null
    fi
  '

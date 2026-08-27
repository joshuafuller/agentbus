# Containerized codex worker for agentbus development (see run.sh).
#
# Why a container: headless codex runs with approvals bypassed; in here
# "full access" means a throwaway filesystem, not the dev box. The
# container clones the repo itself over HTTPS (a worktree mount would
# not survive containerization — its .git file points at host paths),
# so the only thing that crosses the boundary is a seeded ~/.codex
# auth and a GH_TOKEN.
FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates curl \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Pin the codex CLI to the version proven on the host.
RUN npm install -g @openai/codex@0.149.1

RUN useradd -m agent
USER agent
WORKDIR /home/agent/work

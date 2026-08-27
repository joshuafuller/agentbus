# Containerized codex worker for agentbus development (see run.sh).
#
# Why a container: headless codex runs with approvals bypassed; in here
# "full access" means a throwaway filesystem, not the dev box. The
# container clones the repo itself over HTTPS (a worktree mount would
# not survive containerization — its .git file points at host paths),
# so the only thing that crosses the boundary is a seeded ~/.codex
# auth and a GH_TOKEN.
FROM node:22-bookworm-slim

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates curl gcc libc6-dev \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Pin the codex CLI to the version proven on the host.
RUN npm install -g @openai/codex@0.149.1

RUN case "${TARGETARCH}" in \
         amd64) GO_SHA256="5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053" ;; \
         arm64) GO_SHA256="fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49" ;; \
         *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://go.dev/dl/go1.26.5.linux-${TARGETARCH}.tar.gz" -o /tmp/go.tar.gz \
    && echo "${GO_SHA256}  /tmp/go.tar.gz" | sha256sum -c - \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"

RUN useradd -m agent
USER agent
WORKDIR /home/agent/work

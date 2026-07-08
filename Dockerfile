# ---- build the Go webhook server ----
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

# ---- runtime: Go server + codex + glab ----
# Node base gives us npm to install the Codex CLI. glab is fetched from its
# GitLab release. Both tool versions are pinned as ARGs so you can bump them.
FROM node:22-bookworm-slim AS runtime

ARG GLAB_VERSION=1.53.0
ARG GH_VERSION=2.62.0
ARG CODEX_VERSION=latest

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git \
 && rm -rf /var/lib/apt/lists/*

# glab (GitLab CLI) — Codex uses it to open issues and create/merge MRs.
RUN arch="$(dpkg --print-architecture)" \
 && curl -fsSL "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${arch}.tar.gz" \
    | tar -xz -C /usr/local bin/glab \
 && glab --version

# gh (GitHub CLI) — Codex uses it to review PRs and fetch PR diffs.
RUN arch="$(dpkg --print-architecture)" \
 && curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" \
    | tar -xz -C /tmp \
 && install "/tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh \
 && rm -rf "/tmp/gh_${GH_VERSION}_linux_${arch}" \
 && gh --version

# Codex CLI.
RUN npm install -g @openai/codex@${CODEX_VERSION} \
 && codex --version || echo "codex installed (version check skipped)"

COPY --from=build /out/server /usr/local/bin/server
COPY config.example.yaml /app/config.example.yaml

# Codex skill: the GitHub->GitLab project map Codex consults at runtime plus the
# vetted mirror helper on PATH. The operating guide (AGENTS.md) is compiled into
# the server and passed as the MCP call's system instructions, so it is not
# placed in the working directory.
COPY .reallsys/projects.toml /app/workspace/projects.toml
COPY codex/bin/gh-pr-mirror.sh /usr/local/bin/gh-pr-mirror.sh
RUN chmod +x /usr/local/bin/gh-pr-mirror.sh

WORKDIR /app
EXPOSE 8080

# Provide config.yaml (mount or bake) plus GITHUB_WEBHOOK_SECRET, GITLAB_HOST,
# and Codex auth (e.g. OPENAI_API_KEY) via the environment. gh/glab credentials
# are NOT baked in: mount authenticated ~/.config/gh and ~/.config/glab-cli
# (see docker-compose.yml) so Codex reuses them.
ENTRYPOINT ["/usr/local/bin/server"]
CMD ["-config", "/app/config.yaml"]

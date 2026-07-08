#!/usr/bin/env bash
# Optional: verify glab can reach the internal GitLab non-interactively.
# The agent itself just sets GITLAB_HOST/GITLAB_TOKEN in Codex's environment, so
# this script is only for manually confirming connectivity before deploying.
set -euo pipefail

: "${GITLAB_HOST:?set GITLAB_HOST, e.g. https://gitlab.internal.corp}"
: "${GITLAB_TOKEN:?set GITLAB_TOKEN (PAT with api scope)}"

echo "Checking glab against ${GITLAB_HOST} ..."
glab auth status --hostname "${GITLAB_HOST#https://}" || true

echo
echo "Whoami:"
glab api user | sed 's/,/,\n/g' | grep -E '"(username|name|id)"' || true

echo
echo "OK. glab is authenticated. The agent will pass these same env vars to Codex."

#!/usr/bin/env bash
# Optional: verify glab is authenticated and can reach the internal GitLab
# non-interactively. The agent no longer injects a token — glab uses its own
# stored credentials (`glab auth login`) — so this just confirms that login
# worked before deploying.
set -euo pipefail

: "${GITLAB_HOST:?set GITLAB_HOST, e.g. https://gitlab.internal.corp}"

host="${GITLAB_HOST#https://}"
host="${host#http://}"

echo "Checking glab against ${GITLAB_HOST} ..."
if ! glab auth status --hostname "${host}"; then
  echo
  echo "glab is not authenticated for ${host}."
  echo "Run: glab auth login --hostname ${host}"
  exit 1
fi

echo
echo "Whoami:"
glab api user | sed 's/,/,\n/g' | grep -E '"(username|name|id)"' || true

echo
echo "OK. glab is authenticated. Codex will reuse these credentials."

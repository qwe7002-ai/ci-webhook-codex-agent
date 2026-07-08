#!/usr/bin/env bash
#
# gh-pr-mirror.sh — safely mirror a GitHub PR's head onto a branch in an internal
# GitLab repo. This encapsulates the error-prone, security-sensitive part of the
# pr_review / pr_merge_sync playbooks so Codex doesn't have to reconstruct it
# (and can't accidentally leak a token into logs or the shell history).
#
# Usage:
#   gh-pr-mirror.sh <pr_number> <github_owner/repo> <gitlab_mirror_git_url> <branch>
#
# Environment (must be set; never printed):
#   GH_TOKEN or GITHUB_TOKEN   auth for gh / GitHub git fetch
#   GITLAB_TOKEN               auth for the GitLab git push
#
# On success prints:
#   MIRROR_BRANCH: <branch>
#   MIRROR_PUSHED: ok
# On failure prints "ERROR: <reason>" and exits non-zero.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

[ "$#" -eq 4 ] || fail "usage: gh-pr-mirror.sh <pr_number> <owner/repo> <gitlab_git_url> <branch>"
pr_number="$1"; gh_repo="$2"; gitlab_url="$3"; branch="$4"

[ -n "${GITLAB_TOKEN:-}" ] || fail "GITLAB_TOKEN is not set"
: "${GH_TOKEN:=${GITHUB_TOKEN:-}}"
[ -n "$GH_TOKEN" ] || fail "GH_TOKEN/GITHUB_TOKEN is not set"
export GH_TOKEN

case "$gitlab_url" in
  https://*) ;;
  *) fail "gitlab url must be https:// (got a non-https URL)";;
esac
# Build an authenticated push URL without ever echoing it. oauth2:<token> is the
# GitLab convention for token auth over HTTPS.
authed_url="https://oauth2:${GITLAB_TOKEN}@${gitlab_url#https://}"

workdir="$(mktemp -d)"
cleanup() { rm -rf "$workdir"; }
trap cleanup EXIT

# Shallow clone keeps this cheap even for large repos; we only need the PR head.
gh repo clone "$gh_repo" "$workdir" -- --depth 50 >/dev/null 2>&1 \
  || fail "failed to clone $gh_repo"
cd "$workdir"

# Check out the PR head into a local branch, then name it deterministically.
gh pr checkout "$pr_number" --repo "$gh_repo" >/dev/null 2>&1 \
  || fail "failed to checkout PR #$pr_number"
git branch -f "$branch" HEAD

# Push only the mirror branch, force so re-runs (PR updates) stay in sync. The
# credential lives only in this remote URL, which we never print.
git remote add gitlab "$authed_url" >/dev/null 2>&1 || git remote set-url gitlab "$authed_url"
if ! git push -f gitlab "$branch" >/dev/null 2>&1; then
  fail "git push to GitLab mirror failed (check token scope / repo access)"
fi

echo "MIRROR_BRANCH: $branch"
echo "MIRROR_PUSHED: ok"

#!/usr/bin/env bash
#
# gh-pr-mirror.sh — safely mirror a GitHub PR's head onto a branch in an internal
# GitLab repo. This encapsulates the error-prone, security-sensitive part of the
# pr_review / pr_merge_sync playbooks so Codex doesn't have to reconstruct it
# (and can't accidentally leak a credential into logs or the shell history).
#
# Auth is provided by the gh and glab CLIs themselves — GitHub fetches go through
# `gh`, and the GitLab push uses glab as a git credential helper — so no token is
# read from the environment or embedded in a URL here.
#
# Usage:
#   gh-pr-mirror.sh <pr_number> <github_owner/repo> <gitlab_mirror_git_url> <branch>
#
# Prerequisites:
#   `gh auth login`   — GitHub CLI authenticated (fetch/checkout the PR head)
#   `glab auth login` — GitLab CLI authenticated to the mirror host (push)
#
# On success prints:
#   MIRROR_BRANCH: <branch>
#   MIRROR_PUSHED: ok
# On failure prints "ERROR: <reason>" and exits non-zero.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

[ "$#" -eq 4 ] || fail "usage: gh-pr-mirror.sh <pr_number> <owner/repo> <gitlab_git_url> <branch>"
pr_number="$1"; gh_repo="$2"; gitlab_url="$3"; branch="$4"

case "$gitlab_url" in
  https://*) ;;
  *) fail "gitlab url must be https:// (got a non-https URL)";;
esac

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

# Push only the mirror branch, force so re-runs (PR updates) stay in sync. Auth
# comes from glab's git credential helper, so no token ever touches the URL, the
# shell, or the logs. The empty helper first resets any inherited chain.
git remote add gitlab "$gitlab_url" >/dev/null 2>&1 || git remote set-url gitlab "$gitlab_url"
if ! git -c credential.helper= \
        -c 'credential.helper=!glab auth git-credential' \
        push -f gitlab "$branch" >/dev/null 2>&1; then
  fail "git push to GitLab mirror failed (is glab authenticated to the mirror host, with push access?)"
fi

echo "MIRROR_BRANCH: $branch"
echo "MIRROR_PUSHED: ok"

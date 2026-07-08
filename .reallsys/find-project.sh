#!/usr/bin/env bash
#
# find-project.sh — quickly resolve the path of an internal GitLab project.
#
# Given a search term (a repo name, a keyword, or a GitHub owner/repo), this
# queries the internal GitLab and prints the matching project path(s)
# (path_with_namespace, e.g. team/web-mirror) plus their web URL, so you don't
# have to remember or hand-build the intranet path.
#
# Usage:
#   find-project.sh <search-term> [limit]
#   find-project.sh web-mirror
#   find-project.sh octocat/hello-world     # the trailing repo name is used
#
# Environment (never printed):
#   GITLAB_HOST    internal GitLab base URL, e.g. https://gitlab.internal.corp
#   GITLAB_TOKEN   PAT/project token with api scope
#
# Output (one line per match):
#   <path_with_namespace>\t<web_url>
# Prints "ERROR: <reason>" to stderr and exits non-zero on failure, and exits 3
# when the query succeeds but matches nothing.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

[ "$#" -ge 1 ] || fail "usage: find-project.sh <search-term> [limit]"
term="$1"
limit="${2:-20}"

[ -n "${GITLAB_HOST:-}" ]  || fail "GITLAB_HOST is not set"
[ -n "${GITLAB_TOKEN:-}" ] || fail "GITLAB_TOKEN is not set"

# Accept a GitHub-style "owner/repo" and search on the bare repo name, which is
# what usually matches the mirror project on GitLab.
search="${term##*/}"
# URL-encode the search term (space and a few common metacharacters).
enc="${search// /%20}"

host="${GITLAB_HOST%/}"

# Fetch matching projects as JSON. Prefer glab (already authenticated); fall back
# to a plain API call. membership=true keeps results to projects the token can
# actually see; simple=true trims the payload.
fetch() {
  local path="projects?search=${enc}&membership=true&simple=true&order_by=last_activity_at&per_page=${limit}"
  if command -v glab >/dev/null 2>&1; then
    glab api "$path" 2>/dev/null && return 0
  fi
  curl -fsSL -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" "${host}/api/v4/${path}"
}

json="$(fetch)" || fail "GitLab API request failed (check GITLAB_HOST / token scope / network)"

# Extract "path_with_namespace \t web_url" for each match, newest activity first.
if command -v jq >/dev/null 2>&1; then
  out="$(printf '%s' "$json" | jq -r '.[] | "\(.path_with_namespace)\t\(.web_url)"')"
else
  # jq-less fallback: pull the two fields out of the JSON with sed.
  paths="$(printf '%s' "$json" | grep -o '"path_with_namespace":"[^"]*"' | sed 's/.*:"//;s/"$//')"
  urls="$(printf '%s'  "$json" | grep -o '"web_url":"[^"]*"'            | sed 's/.*:"//;s/"$//')"
  out="$(paste <(printf '%s\n' "$paths") <(printf '%s\n' "$urls"))"
fi

out="$(printf '%s\n' "$out" | sed '/^[[:space:]]*$/d')"
[ -n "$out" ] || { echo "no internal project matched: $search" >&2; exit 3; }

printf '%s\n' "$out"

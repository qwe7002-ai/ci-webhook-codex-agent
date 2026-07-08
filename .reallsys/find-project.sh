#!/usr/bin/env bash
#
# find-project.sh — map a GitHub repo to its internal GitLab (git.reall.us)
# project, so you can quickly find the intranet project path/URL.
#
# The mirror keeps the same <owner>/<repo>, only the host changes:
#   https://github.com/telegram-sms/telegram-sms
#     -> https://git.reall.us/telegram-sms/telegram-sms
#
# Usage:
#   find-project.sh <github-url-or-owner/repo> [--url|--git|--path]
#
#   find-project.sh https://github.com/telegram-sms/telegram-sms
#   find-project.sh git@github.com:telegram-sms/telegram-sms.git --git
#   find-project.sh telegram-sms/telegram-sms --path
#
# Output format (default --url), one line:
#   --url   https://git.reall.us/<owner>/<repo>        (web URL, default)
#   --git   https://git.reall.us/<owner>/<repo>.git    (clone/push URL)
#   --path  <owner>/<repo>                             (project path)
#
# The GitLab base URL defaults to https://git.reall.us; override with
# GITLAB_HOST (e.g. GITLAB_HOST=https://git.reall.us). Prints "ERROR: <reason>"
# to stderr and exits non-zero on bad input.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

[ "$#" -ge 1 ] || fail "usage: find-project.sh <github-url-or-owner/repo> [--url|--git|--path]"
input="$1"
fmt="${2:---url}"

host="${GITLAB_HOST:-https://git.reall.us}"
host="${host%/}"
case "$host" in
  http://*|https://*) ;;
  *) host="https://${host}" ;;
esac

# Normalize any GitHub reference down to "<owner>/<repo>".
ref="$input"
ref="${ref%.git}"                 # drop trailing .git
ref="${ref#git@github.com:}"      # scp-style: git@github.com:owner/repo
ref="${ref#ssh://}"               # ssh:// urls
ref="${ref#https://}"             # https urls
ref="${ref#http://}"
ref="${ref#git://}"
ref="${ref#github.com/}"          # drop host if it was a URL
ref="${ref#github.com:}"
ref="${ref#/}"                    # leading slash

# Keep only the first two path segments (owner/repo), dropping /tree/... etc.
owner="${ref%%/*}"
rest="${ref#*/}"
repo="${rest%%/*}"

[ -n "$owner" ] && [ "$owner" != "$ref" ] || fail "cannot parse owner/repo from: $input"
[ -n "$repo" ] || fail "cannot parse owner/repo from: $input"

path="${owner}/${repo}"

case "$fmt" in
  --url|url|"")  printf '%s/%s\n' "$host" "$path" ;;
  --git|git)     printf '%s/%s.git\n' "$host" "$path" ;;
  --path|path)   printf '%s\n' "$path" ;;
  *) fail "unknown format: $fmt (use --url, --git, or --path)" ;;
esac

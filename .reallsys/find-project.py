#!/usr/bin/env python3
"""Map a GitHub repo to its internal GitLab project, driven by a TOML config.

The mirror normally keeps the same <owner>/<repo> and only swaps the host
(github.com -> git.reall.us). projects.toml holds the default host plus any
per-project overrides for repos whose intranet path or host differs.

Usage:
    find-project.py <github-url-or-owner/repo> [--url|--git|--path] [--config PATH]

    find-project.py https://github.com/telegram-sms/telegram-sms
      -> https://git.reall.us/telegram-sms/telegram-sms
    find-project.py telegram-sms/telegram-sms --git
      -> https://git.reall.us/telegram-sms/telegram-sms.git
    find-project.py git@github.com:telegram-sms/telegram-sms.git --path
      -> telegram-sms/telegram-sms

Config resolution: --config, else $REALLSYS_CONFIG, else projects.toml next to
this script. default_host may be overridden by $GITLAB_HOST.
"""
from __future__ import annotations

import argparse
import os
import sys
import tomllib
from pathlib import Path
from urllib.parse import urlsplit


def die(msg: str) -> "None":
    print(f"ERROR: {msg}", file=sys.stderr)
    raise SystemExit(1)


def normalize_repo(ref: str) -> str:
    """Reduce any GitHub reference to '<owner>/<repo>'."""
    r = ref.strip()
    if r.endswith(".git"):
        r = r[: -len(".git")]
    for prefix in ("git@github.com:", "ssh://", "https://", "http://", "git://"):
        if r.startswith(prefix):
            r = r[len(prefix):]
    for prefix in ("github.com/", "github.com:"):
        if r.startswith(prefix):
            r = r[len(prefix):]
    r = r.lstrip("/")
    parts = [p for p in r.split("/") if p]
    if len(parts) < 2:
        die(f"cannot parse owner/repo from: {ref}")
    return f"{parts[0]}/{parts[1]}"


def normalize_host(host: str) -> str:
    host = host.rstrip("/")
    if not host.startswith(("http://", "https://")):
        host = "https://" + host
    return host


def load_config(path: Path) -> tuple[str, dict[str, str]]:
    if not path.is_file():
        die(f"config not found: {path}")
    try:
        with path.open("rb") as fh:
            data = tomllib.load(fh)
    except tomllib.TOMLDecodeError as exc:
        die(f"invalid TOML in {path}: {exc}")
    default_host = os.environ.get("GITLAB_HOST") or data.get("default_host") or ""
    if not default_host:
        die(f"no default_host in {path} and $GITLAB_HOST is unset")
    overrides: dict[str, str] = {}
    for i, proj in enumerate(data.get("projects", [])):
        gh, gl = proj.get("github"), proj.get("gitlab")
        if not gh or not gl:
            die(f"projects[{i}] needs both 'github' and 'gitlab'")
        overrides[gh.strip().lower()] = gl.strip()
    return normalize_host(default_host), overrides


def default_config_path() -> Path:
    env = os.environ.get("REALLSYS_CONFIG")
    if env:
        return Path(env)
    return Path(__file__).resolve().parent / "projects.toml"


def emit(base_url: str, path: str, fmt: str) -> str:
    base_url = base_url.rstrip("/")
    if base_url.endswith(".git"):
        base_url = base_url[: -len(".git")]
    if fmt == "url":
        return base_url
    if fmt == "git":
        return base_url + ".git"
    if fmt == "path":
        return path
    die(f"unknown format: {fmt}")


def main() -> None:
    ap = argparse.ArgumentParser(add_help=True)
    ap.add_argument("repo", help="GitHub URL or <owner>/<repo>")
    fmt = ap.add_mutually_exclusive_group()
    fmt.add_argument("--url", action="store_const", dest="fmt", const="url")
    fmt.add_argument("--git", action="store_const", dest="fmt", const="git")
    fmt.add_argument("--path", action="store_const", dest="fmt", const="path")
    ap.add_argument("--config", type=Path, default=None)
    ap.set_defaults(fmt="url")
    args = ap.parse_args()

    default_host, overrides = load_config(args.config or default_config_path())
    repo_path = normalize_repo(args.repo)

    override = overrides.get(repo_path.lower())
    if override is not None:
        base_url = override
        # Derive the project path from the override URL's path component.
        path = urlsplit(base_url).path.strip("/")
        if path.endswith(".git"):
            path = path[: -len(".git")]
    else:
        base_url = f"{default_host}/{repo_path}"
        path = repo_path

    print(emit(base_url, path, args.fmt))


if __name__ == "__main__":
    main()

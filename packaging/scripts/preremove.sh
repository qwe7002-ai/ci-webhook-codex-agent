#!/bin/sh
# Runs before remove/upgrade: stop and disable the service.
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now ci-webhook-codex-agent.service || true
fi

exit 0

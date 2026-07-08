#!/bin/sh
# Runs after remove/purge: reload systemd. The service user and /var/lib state
# are intentionally left in place (removing them on purge is left to the admin).
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

exit 0

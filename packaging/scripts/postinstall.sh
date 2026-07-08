#!/bin/sh
# Runs after install/upgrade: create the service user, fix ownership/perms,
# register the systemd unit.
set -e

USER=ci-webhook-codex-agent
STATE=/var/lib/$USER
ETC=/etc/$USER

# Dedicated system user/group (no login, no home creation — dir ships in the pkg).
if ! getent group "$USER" >/dev/null 2>&1; then
    addgroup --system "$USER" >/dev/null 2>&1 || groupadd --system "$USER"
fi
if ! getent passwd "$USER" >/dev/null 2>&1; then
    adduser --system --ingroup "$USER" --home "$STATE" --no-create-home \
        --shell /usr/sbin/nologin "$USER" >/dev/null 2>&1 \
        || useradd --system --gid "$USER" --home-dir "$STATE" \
             --shell /usr/sbin/nologin "$USER"
fi

mkdir -p "$STATE"
chown -R "$USER":"$USER" "$STATE"

# Protect the secrets file.
if [ -f "$ETC/agent.env" ]; then
    chown root:"$USER" "$ETC/agent.env" || true
    chmod 0640 "$ETC/agent.env" || true
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    systemctl enable ci-webhook-codex-agent.service || true
fi

cat <<'EOF'

ci-webhook-codex-agent installed.

Next steps:
  1. Install the external CLIs it drives (not in apt):
       codex  https://github.com/openai/codex
       glab   https://gitlab.com/gitlab-org/cli
       gh     https://cli.github.com/
  2. Authenticate gh and glab AS THE SERVICE USER so Codex reuses their
     credentials (HOME=/var/lib/ci-webhook-codex-agent):
       sudo -u ci-webhook-codex-agent -H gh auth login
       sudo -u ci-webhook-codex-agent -H glab auth login --hostname gitlab.internal.corp
  3. Edit /etc/ci-webhook-codex-agent/config.yaml  (events, gitlab, pr.*)
  4. Fill /etc/ci-webhook-codex-agent/agent.env    (webhook secret, GITLAB_HOST)
  5. Start it:
       systemctl start ci-webhook-codex-agent
       systemctl status ci-webhook-codex-agent

EOF

exit 0

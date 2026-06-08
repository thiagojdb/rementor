#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: make install" >&2
  exit 1
fi

SERVER_BIN="$1"
CTL_BIN="$2"
TARGET_HOME="${HOME:-}"

if [[ -z "$TARGET_HOME" ]]; then
  echo "unable to determine home directory" >&2
  exit 1
fi

if [[ ! -x "$SERVER_BIN" ]]; then
  echo "missing server binary: $SERVER_BIN" >&2
  exit 1
fi
if [[ ! -x "$CTL_BIN" ]]; then
  echo "missing ctl binary: $CTL_BIN" >&2
  exit 1
fi

INSTALL_BIN_DIR="$TARGET_HOME/.local/bin"
DATA_DIR="$TARGET_HOME/.local/share/rementor"
CACHE_DIR="$TARGET_HOME/.cache/rementor"
CONFIG_DIR="$TARGET_HOME/.config/rementor"
NGINX_CONF_DIR="$CONFIG_DIR/nginx"
SERVICE_DIR="$TARGET_HOME/.config/systemd/user"
SERVICE_FILE="$SERVICE_DIR/rementor.service"

install -d -m 0755 "$INSTALL_BIN_DIR"
install -d -m 0700 "$DATA_DIR"
install -d -m 0700 "$CACHE_DIR"
install -d -m 0700 "$CONFIG_DIR"
install -d -m 0700 "$NGINX_CONF_DIR"
install -d -m 0755 "$SERVICE_DIR"

install -m 0755 "$SERVER_BIN" "$INSTALL_BIN_DIR/rementor"
install -m 0755 "$CTL_BIN" "$INSTALL_BIN_DIR/rementorctl"

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Rementor service
After=network.target
Wants=network.target

[Service]
Type=notify
NotifyAccess=main
Environment=XDG_DATA_HOME=$TARGET_HOME/.local/share
Environment=XDG_CACHE_HOME=$TARGET_HOME/.cache
WorkingDirectory=$TARGET_HOME/.local/share/rementor
ExecStart=$TARGET_HOME/.local/bin/rementor -host 127.0.0.1
Restart=always
RestartSec=2
WatchdogSec=30

[Install]
WantedBy=default.target
EOF

echo "Installed rementor to $INSTALL_BIN_DIR"
echo "User service file: $SERVICE_FILE"

if systemctl --user daemon-reload >/dev/null 2>&1; then
  if systemctl --user enable --now rementor >/dev/null 2>&1; then
    echo "Enabled user service: rementor"
  else
    echo "Installed user service, but could not enable it automatically" >&2
    echo "Try: systemctl --user enable --now rementor" >&2
  fi
else
  echo "Installed user service, but systemctl --user is not available" >&2
  echo "Try: systemctl --user daemon-reload && systemctl --user enable --now rementor" >&2
fi

echo "nginx route config directory: $NGINX_CONF_DIR"
echo "Persistent data: $DATA_DIR/rementor.db"

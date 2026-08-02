#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: make install" >&2
  exit 1
fi

SERVER_BIN="$1"
CTL_BIN="$2"
TARGET_HOME="${HOME:-}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIST="${REMENTOR_FRONTEND_DIST:-$SCRIPT_DIR/../cmd/server/dist}"

if [[ -z "$TARGET_HOME" ]]; then
  echo "unable to determine home directory" >&2
  exit 1
fi
if [[ ! -f "$FRONTEND_DIST/index.html" ]]; then
  echo "missing frontend build at $FRONTEND_DIST; run 'make frontend' first" >&2
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
FRONTEND_DATA_DIR="$DATA_DIR/frontend"
CACHE_DIR="$TARGET_HOME/.cache/rementor"
CONFIG_DIR="$TARGET_HOME/.config/rementor"
NGINX_CONF_DIR="$CONFIG_DIR/nginx"
SERVICE_DIR="$TARGET_HOME/.config/systemd/user"
SERVICE_FILE="$SERVICE_DIR/rementor.service"
NGINX_MAIN_CONFIG="$CONFIG_DIR/nginx-main.conf"
NGINX_WRAPPER="$INSTALL_BIN_DIR/rementor-nginx"
NGINX_RUNTIME_DIR="$CACHE_DIR/nginx"
NGINX_SERVICE_FILE="$SERVICE_DIR/rementor-nginx.service"
NGINX_BIN="$(command -v nginx || true)"

install -d -m 0755 "$INSTALL_BIN_DIR"
install -d -m 0700 "$DATA_DIR"
install -d -m 0755 "$FRONTEND_DATA_DIR"
install -d -m 0700 "$CACHE_DIR"
install -d -m 0700 "$CONFIG_DIR"
install -d -m 0700 "$NGINX_CONF_DIR"
install -d -m 0700 "$NGINX_RUNTIME_DIR"
install -d -m 0700 "$NGINX_RUNTIME_DIR/client-body"
install -d -m 0700 "$NGINX_RUNTIME_DIR/proxy"
install -d -m 0700 "$NGINX_RUNTIME_DIR/fastcgi"
install -d -m 0700 "$NGINX_RUNTIME_DIR/scgi"
install -d -m 0700 "$NGINX_RUNTIME_DIR/uwsgi"
install -d -m 0755 "$SERVICE_DIR"

install -m 0755 "$SERVER_BIN" "$INSTALL_BIN_DIR/rementor"
install -m 0755 "$CTL_BIN" "$INSTALL_BIN_DIR/rementorctl"
cp -a "$FRONTEND_DIST/." "$FRONTEND_DATA_DIR/"

if [[ -n "$NGINX_BIN" ]]; then
  cat > "$NGINX_MAIN_CONFIG" <<EOF
worker_processes 1;
pid $NGINX_RUNTIME_DIR/nginx.pid;
error_log $NGINX_RUNTIME_DIR/error.log;

events {
    worker_connections 1024;
}

http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    access_log off;
    client_body_temp_path $NGINX_RUNTIME_DIR/client-body;
    proxy_temp_path $NGINX_RUNTIME_DIR/proxy;
    fastcgi_temp_path $NGINX_RUNTIME_DIR/fastcgi;
    scgi_temp_path $NGINX_RUNTIME_DIR/scgi;
    uwsgi_temp_path $NGINX_RUNTIME_DIR/uwsgi;
    underscores_in_headers on;
    include $NGINX_CONF_DIR/*.conf;
}
EOF
  chmod 0600 "$NGINX_MAIN_CONFIG"

  cat > "$NGINX_WRAPPER" <<EOF
#!/usr/bin/env sh
set -eu

if [ "\${1:-}" = "serve" ]; then
    shift
    exec "$NGINX_BIN" -p "$NGINX_RUNTIME_DIR/" -c "$NGINX_MAIN_CONFIG" -g "daemon off;" "\$@"
fi

exec "$NGINX_BIN" -p "$NGINX_RUNTIME_DIR/" -c "$NGINX_MAIN_CONFIG" "\$@"
EOF
  chmod 0755 "$NGINX_WRAPPER"

  cat > "$NGINX_SERVICE_FILE" <<EOF
[Unit]
Description=Rementor unprivileged nginx
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=$NGINX_WRAPPER serve
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF
fi

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Rementor service
After=network.target rementor-nginx.service
Wants=network.target rementor-nginx.service

[Service]
Type=notify
NotifyAccess=main
Environment=XDG_DATA_HOME=$TARGET_HOME/.local/share
Environment=XDG_CACHE_HOME=$TARGET_HOME/.cache
Environment=REMENTOR_NGINX_CONF_DIR=$NGINX_CONF_DIR
Environment=REMENTOR_NGINX_BINARY=$NGINX_WRAPPER
Environment=REMENTOR_NGINX_LISTEN_HOST=127.0.0.1
Environment=REMENTOR_NGINX_LISTEN_PORTS=18080
Environment=REMENTOR_FRONTEND_DIST=$FRONTEND_DATA_DIR
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
  if [[ -n "$NGINX_BIN" ]]; then
    if ! systemctl --user is-active --quiet rementor-nginx && [[ -f "$NGINX_CONF_DIR/workspaces.conf" ]]; then
      mv "$NGINX_CONF_DIR/workspaces.conf" "$NGINX_RUNTIME_DIR/workspaces.conf.pre-standalone"
    fi
    systemctl --user enable rementor-nginx >/dev/null 2>&1
    systemctl --user restart rementor-nginx
  fi
  if systemctl --user enable rementor >/dev/null 2>&1 && systemctl --user restart rementor; then
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
if [[ -n "$NGINX_BIN" ]]; then
  echo "Rementor nginx upstream: http://127.0.0.1:18080"
else
  echo "nginx was not found; Rementor routing is unavailable" >&2
fi
echo "Persistent data: $DATA_DIR/rementor.db"

#!/usr/bin/env bash
set -euo pipefail

SERVER_URL="${RMENTOR_URL:-http://localhost:9300}"
CTL="${REMENTOR_CLI:-rementorctl}"

"$CTL" --server "$SERVER_URL" workspace create demo \
  --name "Demo Workspace" \
  --local-domain api.localhost \
  --default-remote-base-url http://127.0.0.1:18080 2>/dev/null || true

"$CTL" --server "$SERVER_URL" app register demo web \
  --name "Web" \
  --port 28080 \
  --path / \
  --context / \
  --health health \
  --remote-base-url http://127.0.0.1:18080

"$CTL" --server "$SERVER_URL" app register demo orders-api \
  --name "Orders API" \
  --port 28081 \
  --path /orders \
  --context /orders \
  --health health

"$CTL" --server "$SERVER_URL" app register demo billing-api \
  --name "Billing API" \
  --port 28082 \
  --path /billing \
  --context /billing \
  --health health

"$CTL" --server "$SERVER_URL" nginx load-routes 2>/dev/null || true

echo "Demo workspace registered against $SERVER_URL"

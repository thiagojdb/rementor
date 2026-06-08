#!/bin/sh
set -eu

mock-stack &
mock_pid=$!
nginx
rementor &
rementor_pid=$!

cleanup() {
  trap - EXIT INT TERM
  kill "$rementor_pid" "$mock_pid" 2>/dev/null || true
  nginx -s quit 2>/dev/null || true
  wait "$rementor_pid" "$mock_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

until rementorctl workspace list >/dev/null 2>&1; do
  sleep 0.2
done

rementorctl workspace create demo \
  --name "Demo Workspace" \
  --local-domain api.localhost \
  --default-remote-base-url http://127.0.0.1:18080 >/dev/null 2>&1 || true

rementorctl app register demo web \
  --name "Web" --port 28080 --path / --context / --health health \
  --remote-base-url http://127.0.0.1:18080 >/dev/null
rementorctl app register demo orders-api \
  --name "Orders API" --port 28081 --path /orders --context /orders --health health >/dev/null
rementorctl app register demo billing-api \
  --name "Billing API" --port 28082 --path /billing --context /billing --health health >/dev/null

wait "$rementor_pid"

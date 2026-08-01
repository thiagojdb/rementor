#!/usr/bin/env bash
set -euo pipefail

compose() {
  docker compose --project-name rementor-e2e "$@"
}

cleanup() {
  local status=$?
  if ((status != 0)); then
    compose logs --no-color demo >&2 || true
  fi
  compose down --volumes --remove-orphans >&2 || true
  exit "$status"
}
trap cleanup EXIT

request() {
  local path=$1
  curl --fail --silent --show-error \
    --header 'Host: api.localhost' \
    "http://127.0.0.1:8080${path}"
}

assert_source() {
  local path=$1
  local expected=$2
  local response
  response=$(request "$path")
  if [[ "$response" != *"\"source\":\"${expected}\""* ]]; then
    printf 'expected %s to return source=%s; response: %s\n' "$path" "$expected" "$response" >&2
    return 1
  fi
}

compose up --build --detach --wait

assert_source /orders remote
assert_source /billing remote

compose exec --no-TTY demo rementorctl app toggle demo orders-api

assert_source /orders local
assert_source /billing remote

compose exec --no-TTY demo rementorctl app toggle demo orders-api

assert_source /orders remote

printf 'demo routing workflow passed\n'

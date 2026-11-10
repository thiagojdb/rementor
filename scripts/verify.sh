#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
frontend_dist=$(mktemp -d)

cleanup() {
  rm -rf "$frontend_dist"
}
trap cleanup EXIT

generated_manifest() {
  find internal/gen/rementor/v1 web/frontend/src/gen -type f -print0 \
    | sort -z \
    | while IFS= read -r -d '' file; do sha256sum "$file"; done
}

cd "$root_dir"

npm --prefix web/frontend ci
npm --prefix web/frontend audit

buf lint
generated_manifest >"$frontend_dist/generated-before.sha256"
buf generate
generated_manifest >"$frontend_dist/generated-after.sha256"
if ! cmp -s "$frontend_dist/generated-before.sha256" "$frontend_dist/generated-after.sha256"; then
  echo "verify: generated RPC code is stale; commit the output of 'buf generate'" >&2
  exit 1
fi

npm --prefix web/frontend run typecheck
npm --prefix web/frontend run build -- --outDir "$frontend_dist"

go vet ./...
go test ./...
go test -race ./...

if ! command -v docker >/dev/null 2>&1; then
  echo "verify: Docker is required for the demo routing check" >&2
  exit 1
fi

./scripts/test-demo.sh

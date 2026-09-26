#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
check_tmp=$(mktemp -d)
trap 'rm -rf "$check_tmp"' EXIT
cp internal/api/generated.go "$check_tmp/server.go"
cp frontend/src/api.ts "$check_tmp/client.ts"
./scripts/generate.sh
cmp "$check_tmp/server.go" internal/api/generated.go
cmp "$check_tmp/client.ts" frontend/src/api.ts

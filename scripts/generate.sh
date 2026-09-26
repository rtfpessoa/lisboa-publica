#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config api/oapi-codegen.yaml api/openapi.yaml
cd frontend
npm run generate

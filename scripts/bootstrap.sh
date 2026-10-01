#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../experiments/e1-shopping-guide"
go mod download
go test ./... -race
go vet ./...

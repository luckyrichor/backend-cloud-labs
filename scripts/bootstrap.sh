#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
for experiment in e1-shopping-guide e2-room-sync; do
  (cd "experiments/$experiment"; go mod download)
done
bash scripts/check.sh

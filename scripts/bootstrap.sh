#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
for module in experiments/e1-shopping-guide experiments/e2-room-sync experiments/e4-scheduler experiments/e5-cmdb support/telemetry loadgen experiments/e3-rule-engine/httpdemo tools/p0-probe tools/datapipe; do
  (cd "$module"; go mod download)
done
bash scripts/check.sh

#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ${1:-} != --development ]] && [[ -n $(git status --porcelain) ]]; then echo "clean worktree required" >&2; exit 1; fi
mkdir -p .local/bin
for row in 'e1-shopping-guide guide e1' 'e2-room-sync room e2' 'e5-cmdb cmdb e5'; do
 read -r module cmd name <<< "$row"
 (cd "experiments/$module"; CGO_ENABLED=0 go build -trimpath -o "../../.local/bin/$name" "./cmd/$cmd")
done
(cd experiments/e3-rule-engine/httpdemo; CGO_ENABLED=0 go build -trimpath -o ../../../.local/bin/e3 ./cmd/rule-http)
g++ -std=c++20 -O2 -Wall -Wextra -Werror -static experiments/e3-rule-engine/rule_eval.cpp -o .local/bin/rule-eval

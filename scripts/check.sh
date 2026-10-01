#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
container=$(docker run -d --rm -p 127.0.0.1::6379 redis:7-alpine)
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
ready=false
for attempt in {1..30}; do
  if [[ $(docker exec "$container" redis-cli ping) == PONG ]]; then ready=true; break; fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then echo "Redis not ready" >&2; exit 1; fi
export E1_REDIS_TEST_ADDR
E1_REDIS_TEST_ADDR=$(docker port "$container" 6379/tcp)
for experiment in e1-shopping-guide e2-room-sync; do
  (cd "experiments/$experiment"; go test -race -count=1 ./...; go vet ./...)
done

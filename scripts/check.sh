#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
container=$(docker run -d --rm -p 127.0.0.1::6379 redis:7-alpine)
# Throwaway MySQL (same pinned image as deploy/compose.yaml, data on tmpfs) for E1's MySQL-source run.
mysql_image=$(grep -o 'mysql:8.0@sha256:[0-9a-f]*' deploy/compose.yaml)
mysql_password=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')
mysql_container=$(docker run -d --rm -e MYSQL_ROOT_PASSWORD="$mysql_password" -p 127.0.0.1::3306 \
  --tmpfs /var/lib/mysql:rw --memory 768m "$mysql_image" --innodb-buffer-pool-size=64M)
trap 'docker rm -f "$container" "$mysql_container" >/dev/null 2>&1 || true' EXIT
ready=false
for attempt in {1..30}; do
  if [[ $(docker exec "$container" redis-cli ping) == PONG ]]; then ready=true; break; fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then echo "Redis not ready" >&2; exit 1; fi
export E1_REDIS_TEST_ADDR
E1_REDIS_TEST_ADDR=$(docker port "$container" 6379/tcp)
mysql_ready=false
for attempt in {1..120}; do
  if docker exec -e MYSQL_PWD="$mysql_password" "$mysql_container" mysql -uroot -h127.0.0.1 -e 'SELECT 1' >/dev/null 2>&1; then mysql_ready=true; break; fi
  sleep 0.5
done
if [[ "$mysql_ready" != true ]]; then echo "MySQL not ready" >&2; exit 1; fi
for module in experiments/e1-shopping-guide experiments/e2-room-sync experiments/e4-scheduler experiments/e5-cmdb support/telemetry loadgen experiments/e3-rule-engine/httpdemo tools/p0-probe tools/datapipe; do
  (cd "$module"; go test -race -count=1 -json ./...; go vet ./...)
done
# Same E1 suite with the MySQL source (P2-1): each test process gets its own database.
(cd experiments/e1-shopping-guide
 E1_TEST_STORE=mysql E1_MYSQL_TEST_DSN="root:${mysql_password}@tcp($(docker port "$mysql_container" 3306/tcp))/?parseTime=true" \
   go test -race -count=1 -json ./...)
cmake -S experiments/e3-rule-engine -B build/e3 -DCMAKE_BUILD_TYPE=Release
cmake --build build/e3 -j2
ctest --test-dir build/e3 --output-on-failure

module github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide

go 1.27.1

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

require (
	github.com/go-sql-driver/mysql v1.10.1
	github.com/luckyrichor/backend-cloud-labs/support/telemetry v0.0.0
	github.com/redis/go-redis/v9 v9.22.0
)

replace github.com/luckyrichor/backend-cloud-labs/support/telemetry => ../../support/telemetry

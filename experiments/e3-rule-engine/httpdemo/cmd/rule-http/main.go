package main

import (
	demo "github.com/luckyrichor/backend-cloud-labs/experiments/e3-rule-engine/httpdemo"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry"
	"log"
	"net/http"
	"time"
)

func main() {
	adminAddress := telemetry.Address("ADMIN_ADDRESS", "127.0.0.1:9083")
	telemetry.ExitHealthcheck(adminAddress)
	m := &telemetry.Metrics{}
	admin, err := telemetry.Start(adminAddress, m)
	if err != nil {
		log.Fatal(err)
	}
	defer admin.Close()
	s := http.Server{Addr: telemetry.Address("RULE_ADDRESS", "127.0.0.1:8083"), Handler: m.HTTP(demo.Handler(telemetry.Address("RULE_EVAL_BINARY", "build/e3/rule_eval"))), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(s.ListenAndServe())
}

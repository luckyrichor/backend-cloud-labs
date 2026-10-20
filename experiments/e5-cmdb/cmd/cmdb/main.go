package main

import (
	"flag"
	cmdb "github.com/luckyrichor/backend-cloud-labs/e5-cmdb"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry"
	"log"
	"net/http"
	"time"
)

func main() {
	adminAddress := telemetry.Address("ADMIN_ADDRESS", "127.0.0.1:9085")
	telemetry.ExitHealthcheck(adminAddress)
	metrics := &telemetry.Metrics{}
	admin, err := telemetry.Start(adminAddress, metrics)
	if err != nil {
		log.Fatal(err)
	}
	defer admin.Close()
	path := flag.String("state", "cmdb.json", "atomic state/audit file")
	flag.Parse()
	s, err := cmdb.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	server := http.Server{Addr: telemetry.Address("CMDB_ADDRESS", "127.0.0.1:8085"), Handler: metrics.HTTP(cmdb.Handler(s)), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

package main

import (
	"flag"
	cmdb "github.com/luckyrichor/backend-cloud-labs/e5-cmdb"
	"log"
	"net/http"
	"time"
)

func main() {
	path := flag.String("state", "cmdb.json", "atomic state/audit file")
	flag.Parse()
	s, err := cmdb.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	server := http.Server{Addr: "127.0.0.1:8085", Handler: cmdb.Handler(s), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

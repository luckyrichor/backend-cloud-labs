package main

import (
	"context"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e2-room-sync/room"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	adminAddress := telemetry.Address("ADMIN_ADDRESS", "127.0.0.1:9082")
	telemetry.ExitHealthcheck(adminAddress)
	metrics := &telemetry.Metrics{}
	config := room.DefaultConfig(50 * time.Millisecond)
	config.Observer = metrics
	for _, limit := range []struct {
		key    string
		target *int
	}{{"ROOM_PER_IP_CONNECTIONS", &config.PerIPConnections}, {"ROOM_PER_IP_IDENTITIES", &config.PerIPIdentities}, {"ROOM_MAX_CONNECTIONS", &config.MaxConnections}, {"ROOM_MAX_IDENTITIES", &config.MaxIdentities}} {
		n, err := telemetry.PositiveInt(limit.key, *limit.target)
		if err != nil {
			log.Fatal(err)
		}
		*limit.target = n
	}
	server, err := room.NewConfigured(config)
	if err != nil {
		log.Fatal(err)
	}
	admin, err := telemetry.Start(adminAddress, metrics)
	if err != nil {
		log.Fatal(err)
	}
	defer admin.Close()

	address := os.Getenv("ROOM_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8082"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	log.Printf("room listening at %s", listener.Addr())
	if err = server.Serve(ctx, listener); err != nil {
		log.Fatal(err)
	}
}

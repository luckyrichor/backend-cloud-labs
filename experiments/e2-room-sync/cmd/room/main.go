package main

import (
	"context"
	"github.com/luckyrichor/backend-cloud-labs/e2-room-sync/room"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
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
	if err = room.New(50*time.Millisecond).Serve(ctx, listener); err != nil {
		log.Fatal(err)
	}
}

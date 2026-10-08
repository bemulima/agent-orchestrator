package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := New(ctx)
	if err != nil {
		log.Fatalf("failed to init app: %v", err)
	}
	defer application.Close()

	if err := application.Run(ctx); err != nil {
		log.Printf("app stopped: %v", err)
	}
}

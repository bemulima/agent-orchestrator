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
		log.Fatalf("failed to initialize app: %v", err)
	}
	defer application.Close()

	if err := application.Run(ctx); err != nil {
		if err != nil {
			log.Printf("application stopped: %v", err)
		}
	}
}

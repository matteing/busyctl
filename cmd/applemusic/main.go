package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/matteing/busybar-apple-music/internal/applemusic"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := applemusic.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "applemusic: %v\n", err)
		os.Exit(1)
	}
}

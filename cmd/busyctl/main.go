package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/matteing/busyctl/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "busyctl: %v\n", err)
		os.Exit(1)
	}
}

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/matteing/busybar-apps/internal/apps/applemusic"
)

const version = "0.1.0"

type appCommand struct {
	name        string
	description string
	run         func(context.Context, []string) error
}

var commands = []appCommand{
	{
		name:        "apple-music",
		description: "Show Apple Music album art and now-playing details",
		run:         applemusic.Run,
	},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		selected, err := selectApp(in, out)
		if err != nil {
			return err
		}
		return selected.run(ctx, nil)
	}

	switch args[0] {
	case "apps", "list":
		printApps(out)
		return nil
	case "help", "-h", "--help":
		printHelp(out)
		return nil
	case "version", "--version":
		fmt.Fprintf(out, "busybar %s\n", version)
		return nil
	case "run":
		if len(args) < 2 {
			return fmt.Errorf("choose an app: busybar run <app>")
		}
		return runNamed(ctx, args[1], args[2:])
	default:
		// The short form `busybar apple-music` is equivalent to `busybar run apple-music`.
		return runNamed(ctx, args[0], args[1:])
	}
}

func runNamed(ctx context.Context, name string, args []string) error {
	for _, command := range commands {
		if command.name == name {
			return command.run(ctx, args)
		}
	}
	return fmt.Errorf("unknown app %q; run `busybar apps` to see available apps", name)
}

func selectApp(in io.Reader, out io.Writer) (appCommand, error) {
	fmt.Fprintln(out, "BUSY Bar Apps")
	fmt.Fprintln(out)
	for i, command := range commands {
		fmt.Fprintf(out, "  %d. %-14s %s\n", i+1, command.name, command.description)
	}
	fmt.Fprint(out, "\nSelect an app [1]: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return appCommand{}, err
		}
		return commands[0], nil
	}
	choice := strings.TrimSpace(scanner.Text())
	if choice == "" || choice == "1" || choice == commands[0].name {
		return commands[0], nil
	}
	return appCommand{}, fmt.Errorf("invalid selection %q", choice)
}

func printApps(out io.Writer) {
	for _, command := range commands {
		fmt.Fprintf(out, "%-14s %s\n", command.name, command.description)
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, `busybar runs host-side apps on a BUSY Bar.

Usage:
  busybar                         Select an app interactively
  busybar apps                    List available apps
  busybar run <app> [flags]       Run an app
  busybar <app> [flags]           Short form
  busybar version                 Print the version

Run "busybar <app> --help" for app-specific options.`)
}

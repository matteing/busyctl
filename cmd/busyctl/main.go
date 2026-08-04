package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/matteing/busybar-apps/internal/apps/applemusic"
	"github.com/matteing/busybar-apps/internal/apps/hackernews"
	barapi "github.com/matteing/busybar-apps/internal/busybar"
)

// version is replaced from the release tag by the GitHub Actions build.
var version = "dev"

type appCommand struct {
	name        string
	application string
	description string
	run         func(context.Context, []string) error
}

var commands = []appCommand{
	{
		name:        "apple-music",
		application: applemusic.ApplicationID,
		description: "Show Apple Music album art and now-playing details",
		run:         applemusic.Run,
	},
	{
		name:        "hacker-news",
		application: hackernews.ApplicationID,
		description: "Show three live Hacker News headlines with an animated HN glow",
		run:         hackernews.Run,
	},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "busyctl: %v\n", err)
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
		if len(args) > 1 {
			return fmt.Errorf("%s does not accept arguments", args[0])
		}
		printApps(out)
		return nil
	case "run":
		if len(args) < 2 {
			return errors.New("missing app name; usage: busyctl run <app> [flags]")
		}
		return runNamed(ctx, args[1], args[2:])
	case "status":
		return deviceStatus(ctx, args[1:], out)
	case "clear":
		return clearDisplay(ctx, args[1:], out)
	case "help", "-h", "--help":
		if len(args) > 1 {
			return runNamed(ctx, args[1], []string{"--help"})
		}
		printHelp(out)
		return nil
	case "version", "--version", "-v":
		if len(args) > 1 {
			return errors.New("version does not accept arguments")
		}
		fmt.Fprintf(out, "busyctl %s\n", version)
		return nil
	default:
		// `busyctl apple-music` is the convenient form of
		// `busyctl run apple-music`.
		return runNamed(ctx, args[0], args[1:])
	}
}

func runNamed(ctx context.Context, name string, args []string) error {
	command, ok := findApp(name)
	if !ok {
		return fmt.Errorf("unknown app %q; run `busyctl apps` to see available apps", name)
	}
	return command.run(ctx, args)
}

func findApp(name string) (appCommand, bool) {
	for _, command := range commands {
		if command.name == name {
			return command, true
		}
	}
	return appCommand{}, false
}

func selectApp(in io.Reader, out io.Writer) (appCommand, error) {
	fmt.Fprintln(out, "BUSY Bar Control")
	fmt.Fprintln(out)
	for i, command := range commands {
		fmt.Fprintf(out, "  %d  %-14s  %s\n", i+1, command.name, command.description)
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
	if choice == "" {
		return commands[0], nil
	}
	if index, err := strconv.Atoi(choice); err == nil {
		if index >= 1 && index <= len(commands) {
			return commands[index-1], nil
		}
		return appCommand{}, fmt.Errorf("selection %d is out of range", index)
	}
	if command, ok := findApp(choice); ok {
		return command, nil
	}
	return appCommand{}, fmt.Errorf("unknown app %q", choice)
}

func printApps(out io.Writer) {
	fmt.Fprintln(out, "AVAILABLE APPS")
	for _, command := range commands {
		fmt.Fprintf(out, "  %-14s  %s\n", command.name, command.description)
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, `busyctl controls host-side apps for a BUSY Bar.

Usage:
  busyctl                              Select and run an app interactively
  busyctl apps                         List available apps
  busyctl run <app> [flags]            Run an app
  busyctl <app> [flags]                Run an app (short form)
  busyctl status [device flags]        Check device connectivity
  busyctl clear [device flags] <app>   Clear one app from the display
  busyctl clear --all [device flags]   Clear the entire display
  busyctl help [app]                   Show CLI or app help
  busyctl version                      Print the version

Device flags:
  --host <host>    BUSY Bar hostname or IP (default: BUSYBAR_HOST or 10.0.4.20)
  --token <token>  Wi-Fi API token (default: BUSYBAR_TOKEN)

Examples:
  busyctl apple-music
  busyctl apple-music --poll 10s
  busyctl hacker-news
  busyctl status
  busyctl clear apple-music
  busyctl clear --host 192.168.1.50 apple-music

Apps run until interrupted. Press Ctrl+C to stop.`)
}

type deviceOptions struct {
	host  string
	token string
}

func parseDeviceFlags(name, usage string, args []string, allowAll bool, out io.Writer) (deviceOptions, bool, []string, error) {
	var options deviceOptions
	var all bool
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&options.host, "host", envOr("BUSYBAR_HOST", "10.0.4.20"), "BUSY Bar hostname or IP")
	flags.StringVar(&options.token, "token", envOr("BUSYBAR_TOKEN", ""), "Wi-Fi API token")
	if allowAll {
		flags.BoolVar(&all, "all", false, "clear the entire display")
	}
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s\n\nOptions:\n", usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return options, false, nil, err
	}
	return options, all, flags.Args(), nil
}

func deviceStatus(ctx context.Context, args []string, out io.Writer) error {
	options, _, rest, err := parseDeviceFlags("status", "busyctl status [device flags]", args, false, out)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(rest) != 0 {
		return errors.New("status does not accept positional arguments")
	}
	client := barapi.New(options.host, options.token)
	info, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("device at %s is unreachable: %w", options.host, err)
	}
	fmt.Fprintf(out, "BUSY Bar is reachable\n  Host: %s\n  API:  %s\n  Apps: %d available\n", options.host, info.APISemver, len(commands))
	return nil
}

func clearDisplay(ctx context.Context, args []string, out io.Writer) error {
	options, all, rest, err := parseDeviceFlags("clear", "busyctl clear [device flags] <app>\n       busyctl clear --all [device flags]", args, true, out)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if all && len(rest) != 0 {
		return errors.New("choose either an app name or --all, not both")
	}
	if !all && len(rest) != 1 {
		return errors.New("missing app name; usage: busyctl clear <app> or busyctl clear --all")
	}

	client := barapi.New(options.host, options.token)
	if _, err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", options.host, err)
	}
	if all {
		if err := client.ClearAll(ctx); err != nil {
			return fmt.Errorf("clear display: %w", err)
		}
		fmt.Fprintln(out, "Cleared the entire BUSY Bar display.")
		return nil
	}

	command, ok := findApp(rest[0])
	if !ok {
		return fmt.Errorf("unknown app %q; run `busyctl apps` to see available apps", rest[0])
	}
	if err := client.Clear(ctx, command.application); err != nil {
		return fmt.Errorf("clear %s: %w", command.name, err)
	}
	fmt.Fprintf(out, "Cleared %s from the BUSY Bar display.\n", command.name)
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

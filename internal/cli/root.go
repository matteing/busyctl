package cli

import (
	"context"

	"github.com/matteing/busyctl/internal/applemusic"
	"github.com/matteing/busyctl/internal/clock"
	"github.com/spf13/cobra"
)

var version = "dev"

type AppleMusicRunner func(context.Context, applemusic.Config) error
type ClockRunner func(context.Context, clock.Config) error

type deviceFlags struct {
	host        string
	token       string
	priority    int
	keepDisplay bool
}

// NewRootCommand builds the complete busyctl command tree. The runner is
// injectable so command parsing and help can be tested without device I/O.
func NewRootCommand(runAppleMusic AppleMusicRunner) *cobra.Command {
	return newRootCommand(runAppleMusic, nil)
}

func newRootCommand(runAppleMusic AppleMusicRunner, runClock ClockRunner) *cobra.Command {
	if runAppleMusic == nil {
		runAppleMusic = applemusic.Run
	}
	if runClock == nil {
		runClock = clock.Run
	}
	defaults := applemusic.DefaultConfig()
	device := deviceFlags{
		host:     defaults.Host,
		token:    defaults.Token,
		priority: defaults.Priority,
	}
	root := &cobra.Command{
		Use:           "busyctl",
		Short:         "Run custom apps on a BUSY Bar",
		Long:          "busyctl is a single, scriptable home for custom BUSY Bar apps.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetVersionTemplate("busyctl {{.Version}}\n")

	flags := root.PersistentFlags()
	flags.StringVar(&device.host, "host", device.host, "BUSY Bar hostname or IP")
	flags.StringVar(&device.token, "token", device.token, "Wi-Fi API token (or BUSYBAR_TOKEN)")
	// pflag normally prints a non-empty string default. Keep an environment
	// token usable but never reveal it in help output.
	flags.Lookup("token").DefValue = ""
	flags.IntVar(&device.priority, "priority", device.priority, "display priority")
	flags.BoolVar(&device.keepDisplay, "keep-display", false, "leave the final frame on exit")

	root.AddCommand(newAppleMusicCommand(&device, defaults, runAppleMusic))
	root.AddCommand(newClockCommand(&device, clock.DefaultConfig(), runClock))
	return root
}

// Execute runs busyctl with an explicit argument slice for use by main and
// embedders. ExecuteContext propagates Ctrl+C cancellation into active apps.
func Execute(ctx context.Context, args []string) error {
	command := NewRootCommand(nil)
	command.SetArgs(args)
	return command.ExecuteContext(ctx)
}

package cli

import (
	"github.com/matteing/busyctl/internal/clock"
	"github.com/spf13/cobra"
)

func newClockCommand(device *deviceFlags, defaults clock.Config, run ClockRunner) *cobra.Command {
	config := defaults
	command := &cobra.Command{
		Use:   "clock",
		Short: "Show a large local-time clock on the BUSY Bar",
		Args:  cobra.NoArgs,
		Example: "  busyctl clock\n" +
			"  busyctl clock --seconds\n" +
			"  busyctl clock --12-hour=false --seconds --blink-colon",
		RunE: func(command *cobra.Command, _ []string) error {
			config.Host = device.host
			config.Token = device.token
			config.Priority = device.priority
			config.KeepDisplay = device.keepDisplay
			if err := config.Validate(); err != nil {
				return err
			}
			return run(command.Context(), config)
		},
	}
	flags := command.Flags()
	flags.BoolVar(&config.TwelveHour, "12-hour", defaults.TwelveHour, "use 12-hour time with an AM/PM indicator (disable with --12-hour=false)")
	flags.BoolVar(&config.ShowSeconds, "seconds", defaults.ShowSeconds, "show seconds")
	flags.BoolVar(&config.BlinkColon, "blink-colon", defaults.BlinkColon, "smoothly fade colons once per second (disable with --blink-colon=false)")
	return command
}

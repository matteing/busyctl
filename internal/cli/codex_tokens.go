package cli

import (
	"github.com/matteing/busyctl/internal/codextokens"
	"github.com/spf13/cobra"
)

func newCodexTokensCommand(device *deviceFlags, defaults codextokens.Config, run CodexTokensRunner) *cobra.Command {
	config := defaults
	command := &cobra.Command{
		Use:   "tokens",
		Short: "Show live local Codex token activity on the BUSY Bar",
		Args:  cobra.NoArgs,
		Example: "  busyctl tokens\n" +
			"  busyctl tokens --view count\n" +
			"  busyctl tokens --database ~/.codex/state_5.sqlite\n" +
			"  busyctl tokens --poll 5s",
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
	flags.StringVar(&config.Database, "database", defaults.Database, "Codex state database (or CODEX_STATE_DB)")
	flags.DurationVar(&config.PollInterval, "poll", defaults.PollInterval, "database refresh interval")
	flags.StringVar(&config.View, "view", defaults.View, "display view: graph or count")
	return command
}

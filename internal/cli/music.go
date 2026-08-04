package cli

import (
	"github.com/matteing/busyctl/internal/applemusic"
	"github.com/spf13/cobra"
)

func newAppleMusicCommand(device *deviceFlags, defaults applemusic.Config, run AppleMusicRunner) *cobra.Command {
	config := defaults
	command := &cobra.Command{
		Use:     "music",
		Aliases: []string{"apple-music", "applemusic"},
		Short:   "Show Apple Music now-playing on the BUSY Bar",
		Args:    cobra.NoArgs,
		Example: "  busyctl music\n  busyctl music --view visualizer",
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
	flags.StringVar(&config.Source, "source", defaults.Source, "now-playing JSON endpoint")
	flags.StringVar(&config.View, "view", defaults.View, "view to display: titles or visualizer")
	_ = command.RegisterFlagCompletionFunc("view", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{applemusic.ViewTitles, applemusic.ViewVisualizer}, cobra.ShellCompDirectiveNoFileComp
	})
	return command
}

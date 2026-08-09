package cli

import (
	"fmt"

	"github.com/matteing/busyctl/internal/minecraft"
	"github.com/spf13/cobra"
)

func newMinecraftCommand(device *deviceFlags, defaults minecraft.Config, run MinecraftRunner) *cobra.Command {
	config := defaults
	command := &cobra.Command{
		Use:     "minecraft [server]",
		Aliases: []string{"mc"},
		Short:   "Show live Minecraft server status on the BUSY Bar",
		Args:    cobra.MaximumNArgs(1),
		Example: "  busyctl minecraft\n  busyctl mc\n  busyctl minecraft play.example.net:25565",
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 0 {
				if command.Flags().Changed("server") {
					return fmt.Errorf("provide the Minecraft server as either an argument or --server, not both")
				}
				config.Server = args[0]
			}
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
	flags.StringVar(&config.Server, "server", defaults.Server, "Minecraft Java server hostname or host:port")
	flags.DurationVar(&config.PollInterval, "poll", defaults.PollInterval, "server status refresh interval")
	return command
}

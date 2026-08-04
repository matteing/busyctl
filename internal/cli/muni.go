package cli

import (
	"fmt"

	"github.com/matteing/busyctl/internal/muni"
	"github.com/spf13/cobra"
)

func newMuniCommand(device *deviceFlags, defaults muni.Config, run MuniRunner) *cobra.Command {
	config := defaults
	command := &cobra.Command{
		Use:   "muni [location]",
		Short: "Show live Muni Metro arrivals on the BUSY Bar",
		Args:  cobra.MaximumNArgs(1),
		Example: "  busyctl muni\n" +
			"  busyctl muni openai\n" +
			"  busyctl muni --location LAT,LON",
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 0 {
				if command.Flags().Changed("location") {
					return fmt.Errorf("provide location as either an argument or --location, not both")
				}
				config.Location = args[0]
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
	command.Flags().StringVar(&config.Location, "location", defaults.Location, "auto-detect, or use openai or LAT,LON")
	command.Flags().StringVar(&config.Locator, "location-source", defaults.Locator, "runtime geolocation JSON endpoint")
	command.Flags().BoolVar(&config.AllowNetworkLocation, "allow-network-location", defaults.AllowNetworkLocation, "allow auto mode to send your IP to the location service")
	command.Flags().StringVar(&config.Source, "source", defaults.Source, "SFMTA prediction API base URL")
	return command
}

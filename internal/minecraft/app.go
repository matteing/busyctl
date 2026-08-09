// Package minecraft renders live Java server status on a BUSY Bar.
package minecraft

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

const (
	ApplicationID       = "busybar_minecraft"
	defaultServer       = "homelab"
	defaultPollInterval = 10 * time.Second
	statusTimeout       = 4 * time.Second
	logoFrameCount      = 32
	logoFrameInterval   = 100 * time.Millisecond
	displayWidth        = 72
	outerPadding        = 1
	logoSize            = 14
	logoX               = outerPadding
	logoY               = outerPadding
	contentGap          = 2
	contentX            = logoX + logoSize + contentGap
	iconSize            = 5
	statusIconY         = 2
	playersIconY        = 9
	textX               = contentX + iconSize + 1
	textWidth           = displayWidth - textX - outerPadding
	statusTextY         = 0
	playersTextY        = 7
	onlinePath          = "status-online.png"
	offlinePath         = "status-offline.png"
	checkingPath        = "status-checking.png"
	playersPath         = "players.png"
	statusOnlineColor   = "#70E36FFF"
	statusOfflineColor  = "#FF615BFF"
	statusCheckingColor = "#FFD166FF"
	playersColor        = "#9CA3ACFF"
)

// Config contains the BUSY Bar and Minecraft server settings supplied by busyctl.
type Config struct {
	Host         string
	Token        string
	Server       string
	Priority     int
	KeepDisplay  bool
	PollInterval time.Duration
}

// DefaultConfig targets Sergio's server and the BUSY Bar USB address.
func DefaultConfig() Config {
	return Config{
		Host:         envOr("BUSYBAR_HOST", "10.0.4.20"),
		Token:        envOr("BUSYBAR_TOKEN", ""),
		Server:       envOr("MINECRAFT_SERVER", defaultServer),
		Priority:     100,
		PollInterval: defaultPollInterval,
	}
}

// Validate rejects device values and server addresses that cannot be queried.
func (config Config) Validate() error {
	if config.Priority < 1 || config.Priority > 100 {
		return fmt.Errorf("priority %d is outside 1-100", config.Priority)
	}
	if _, _, _, err := parseServerAddress(config.Server); err != nil {
		return err
	}
	if config.PollInterval < time.Second {
		return fmt.Errorf("poll interval %s is shorter than 1s", config.PollInterval)
	}
	return nil
}

type bar interface {
	UploadAsset(context.Context, string, string, []byte) error
	Draw(context.Context, barapi.Drawing) error
	Clear(context.Context, string) error
}

type displayMode uint8

const (
	modeChecking displayMode = iota
	modeOnline
	modeOffline
)

type displaySnapshot struct {
	Mode          displayMode
	PlayersOnline int
	PlayersMax    int
}

type application struct {
	bar          bar
	fetcher      statusFetcher
	server       string
	priority     int
	pollInterval time.Duration
	sparkleEvery time.Duration
	logoFrame    int
	snapshot     displaySnapshot
}

type pollResult struct {
	status Status
	err    error
}

// Run connects to the BUSY Bar, uploads the fixed pixel-art assets, and polls
// the Minecraft server until the command context is canceled.
func Run(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	device := barapi.New(config.Host, config.Token)
	version, err := device.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", config.Host, err)
	}
	if err := device.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("reset BUSY Bar Minecraft display: %w", err)
	}
	for _, asset := range minecraftAssets() {
		if err := device.UploadAsset(ctx, ApplicationID, asset.path, asset.payload); err != nil {
			return fmt.Errorf("upload %s: %w", asset.path, err)
		}
	}
	if !config.KeepDisplay {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = device.Clear(cleanup, ApplicationID)
		}()
	}

	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", config.Host, version.APISemver)
	fmt.Printf("Minecraft status is watching %s every %s.\n", config.Server, config.PollInterval)
	app := &application{
		bar: device, fetcher: newStatusClient(statusTimeout), server: config.Server,
		priority: config.Priority, pollInterval: config.PollInterval, sparkleEvery: logoFrameInterval,
	}
	return app.run(ctx)
}

func (a *application) run(ctx context.Context) error {
	a.snapshot = displaySnapshot{Mode: modeChecking}
	if err := a.draw(ctx, a.snapshot); err != nil {
		return fmt.Errorf("draw initial Minecraft status: %w", err)
	}

	results := make(chan pollResult, 1)
	polling := false
	startPoll := func() {
		if polling {
			return
		}
		polling = true
		go func() {
			status, err := a.fetcher.Fetch(ctx, a.server)
			select {
			case results <- pollResult{status: status, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()
	sparkleEvery := a.sparkleEvery
	if sparkleEvery <= 0 {
		sparkleEvery = logoFrameInterval
	}
	sparkle := time.NewTicker(sparkleEvery)
	defer sparkle.Stop()
	startPoll()
	warningActive := false
	drawWarningActive := false
	refresh := func() {
		err := a.draw(ctx, a.snapshot)
		if err != nil && !drawWarningActive {
			fmt.Fprintf(os.Stderr, "warning: refresh Minecraft display: %v\n", err)
		}
		drawWarningActive = err != nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			startPoll()
		case <-sparkle.C:
			a.logoFrame = (a.logoFrame + 1) % logoFrameCount
			refresh()
		case result := <-results:
			polling = false
			next := displaySnapshot{Mode: modeOffline}
			if result.err != nil {
				if !warningActive {
					fmt.Fprintf(os.Stderr, "warning: query Minecraft server %s: %v\n", a.server, result.err)
				}
				warningActive = true
			} else {
				warningActive = false
				if result.status.Online {
					next = displaySnapshot{
						Mode: modeOnline, PlayersOnline: result.status.PlayersOnline,
						PlayersMax: result.status.PlayersMax,
					}
				}
			}
			a.snapshot = next
			refresh()
		}
	}
}

// draw commits a complete scene for each gentle logo frame. The periodic
// refresh also lets the app reclaim the display after a temporary priority
// conflict or an external clear.
func (a *application) draw(ctx context.Context, snapshot displaySnapshot) error {
	statusText, statusColor, statusAsset := "CHECKING", statusCheckingColor, checkingPath
	playersText := "-- / --"
	logoAsset := logoPath(a.logoFrame)
	switch snapshot.Mode {
	case modeOnline:
		statusText, statusColor, statusAsset = "ONLINE", statusOnlineColor, onlinePath
		playersText = fmt.Sprintf("%d / %d", snapshot.PlayersOnline, snapshot.PlayersMax)
	case modeOffline:
		statusText, statusColor, statusAsset = "OFFLINE", statusOfflineColor, offlinePath
		logoAsset = offlineLogoPath(a.logoFrame)
	}
	zero := 0
	opacity := 100
	elements := []barapi.Element{
		{ID: "logo", Type: "image", X: logoX, Y: logoY, Path: logoAsset, Display: "front", Opacity: &opacity},
		{ID: "status-icon", Type: "image", X: contentX, Y: statusIconY, Path: statusAsset, Display: "front", Opacity: &opacity},
		{
			ID: "status", Type: "text", X: textX, Y: statusTextY, Text: statusText,
			Font: "small", Color: statusColor, Display: "front", Width: textWidth, ScrollRate: &zero,
		},
		{ID: "players-icon", Type: "image", X: contentX, Y: playersIconY, Path: playersPath, Display: "front", Opacity: &opacity},
		{
			ID: "players", Type: "text", X: textX, Y: playersTextY, Text: playersText,
			Font: "small", Color: playersColor, Display: "front", Width: textWidth, ScrollRate: &zero,
		},
	}
	if err := a.bar.Draw(ctx, barapi.Drawing{
		ApplicationName: ApplicationID,
		Priority:        a.priority,
		Elements:        elements,
	}); err != nil {
		return err
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

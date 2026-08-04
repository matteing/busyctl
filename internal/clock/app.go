// Package clock renders a large local-time clock on a BUSY Bar.
package clock

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

const (
	ApplicationID = "busybar_clock"
	displayWidth  = 72
	baselineY     = 15
	clockFont     = "extra_large"
	periodFont    = "small"
	visibleColor  = "#FFFFFFFF"
	hiddenColor   = "#FFFFFF00"
)

// Config contains the device and clock-face options supplied by busyctl.
type Config struct {
	Host        string
	Token       string
	Priority    int
	KeepDisplay bool
	TwelveHour  bool
	ShowSeconds bool
	BlinkColon  bool
}

// DefaultConfig matches the referenced community clock: 24-hour local time,
// seconds visible, and steady colons.
func DefaultConfig() Config {
	return Config{
		Host:        envOr("BUSYBAR_HOST", "10.0.4.20"),
		Token:       envOr("BUSYBAR_TOKEN", ""),
		Priority:    100,
		ShowSeconds: true,
	}
}

// Validate rejects priorities the BUSY Bar API cannot represent.
func (config Config) Validate() error {
	if config.Priority < 1 || config.Priority > 100 {
		return fmt.Errorf("priority %d is outside 1-100", config.Priority)
	}
	return nil
}

type bar interface {
	Connect(context.Context) (barapi.VersionInfo, error)
	Draw(context.Context, barapi.Drawing) error
	Clear(context.Context, string) error
}

// Run connects to the bar, resets an older clock composition, and redraws only
// when the visible clock state changes.
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
		return fmt.Errorf("reset BUSY Bar clock display: %w", err)
	}
	if !config.KeepDisplay {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = device.Clear(cleanup, ApplicationID)
		}()
	}

	format := "24-hour"
	if config.TwelveHour {
		format = "12-hour AM/PM"
	}
	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", config.Host, version.APISemver)
	fmt.Printf("Clock is active in %s format; seconds=%t, blinking-colon=%t.\n", format, config.ShowSeconds, config.BlinkColon)
	return run(ctx, device, config, time.Now)
}

func run(ctx context.Context, device bar, config Config, now func() time.Time) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	lastState := ""
	warningActive := false

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			current := now()
			elements, state := clockFrame(current, config)
			if state != lastState {
				err := device.Draw(ctx, barapi.Drawing{
					ApplicationName: ApplicationID,
					Priority:        config.Priority,
					Elements:        elements,
				})
				if err != nil {
					if !warningActive {
						fmt.Fprintf(os.Stderr, "warning: draw clock: %v\n", err)
					}
					warningActive = true
				} else {
					lastState = state
					warningActive = false
				}
			}
			timer.Reset(nextDelay(now(), config))
		}
	}
}

type textSegment struct {
	id    string
	text  string
	font  string
	color string
	gap   int
	width int
}

// clockFrame preserves the reference face exactly in its default mode. The
// split elements occupy the same measured width as one HH:MM:SS string, which
// lets blinking colons become transparent without shifting any digit.
func clockFrame(now time.Time, config Config) ([]barapi.Element, string) {
	hour := now.Hour()
	period := ""
	if config.TwelveHour {
		period = "AM"
		if hour >= 12 {
			period = "PM"
		}
		hour %= 12
		if hour == 0 {
			hour = 12
		}
	}

	colonColor := visibleColor
	colonOn := true
	if config.BlinkColon && now.Nanosecond() >= int(500*time.Millisecond) {
		colonColor = hiddenColor
		colonOn = false
	}
	segments := []textSegment{
		newSegment("hours", fmt.Sprintf("%02d", hour), clockFont, visibleColor, 0),
		newSegment("colon-hours", ":", clockFont, colonColor, 0),
		newSegment("minutes", fmt.Sprintf("%02d", now.Minute()), clockFont, visibleColor, 0),
	}
	if config.ShowSeconds {
		segments = append(segments,
			newSegment("colon-minutes", ":", clockFont, colonColor, 0),
			newSegment("seconds", fmt.Sprintf("%02d", now.Second()), clockFont, visibleColor, 0),
		)
	}
	if period != "" {
		font := clockFont
		if config.ShowSeconds {
			font = periodFont
		}
		segments = append(segments, newSegment("meridiem", period, font, visibleColor, 2))
	}

	totalWidth := 0
	for _, segment := range segments {
		totalWidth += segment.gap + segment.width
	}
	left := displayWidth/2 - (totalWidth+1)/2
	elements := make([]barapi.Element, 0, len(segments))
	for _, segment := range segments {
		left += segment.gap
		elements = append(elements, barapi.Element{
			ID:      segment.id,
			Type:    "text",
			X:       left,
			Y:       baselineY,
			Text:    segment.text,
			Font:    segment.font,
			Align:   "bottom_left",
			Color:   segment.color,
			Display: "front",
		})
		left += segment.width
	}
	state := fmt.Sprintf("%02d:%02d:%02d:%s:%t", hour, now.Minute(), now.Second(), period, colonOn)
	return elements, state
}

func newSegment(id, text, font, color string, gap int) textSegment {
	return textSegment{id: id, text: text, font: font, color: color, gap: gap, width: textWidth(font, text)}
}

// textWidth mirrors the bundled firmware atlas advances used by the reference
// extra-large clock. Only glyphs emitted by this package are accepted.
func textWidth(font, value string) int {
	width := 0
	for _, character := range value {
		switch font {
		case clockFont:
			switch character {
			case '1':
				width += 5
			case '0', '2', '3', '4', '5', '6', '7', '8', '9', 'A', 'P':
				width += 8
			case 'M':
				width += 9
			case ':':
				width += 3
			default:
				width += 4
			}
		case periodFont:
			switch character {
			case 'M':
				width += 6
			default:
				width += 5
			}
		}
	}
	return width
}

func nextDelay(now time.Time, config Config) time.Duration {
	precision := time.Minute
	if config.ShowSeconds {
		precision = time.Second
	}
	if config.BlinkColon {
		precision = 500 * time.Millisecond
	}
	delay := now.Truncate(precision).Add(precision).Sub(now)
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

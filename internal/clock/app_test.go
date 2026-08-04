package clock

import (
	"testing"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

func TestDefaultFrameMatchesReferenceClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 4, 10, 26, 9, 0, time.Local)
	config := DefaultConfig()
	config.TwelveHour = false
	config.ShowSeconds = true
	config.BlinkColon = false
	elements, _ := clockFrame(now, config)
	if got := joinedText(elements); got != "10:26:09" {
		t.Fatalf("clock text = %q, want 10:26:09", got)
	}
	if len(elements) != 5 {
		t.Fatalf("clock elements = %d, want 5", len(elements))
	}
	first, last := elements[0], elements[len(elements)-1]
	if first.X != 10 || last.X+textWidth(last.Font, last.Text) != 61 {
		t.Fatalf("clock bounds = %d..%d, want 10..61", first.X, last.X+textWidth(last.Font, last.Text))
	}
	for _, element := range elements {
		if element.Y != 15 || element.Font != clockFont || element.Align != "bottom_left" || element.Color != visibleColor {
			t.Fatalf("reference element = %#v", element)
		}
	}
}

func TestDefaultsUseMeridiemWithoutSeconds(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	if !config.TwelveHour || config.ShowSeconds || !config.BlinkColon {
		t.Fatalf("clock defaults = twelve-hour %t, seconds %t, fading colon %t", config.TwelveHour, config.ShowSeconds, config.BlinkColon)
	}
	elements, _ := clockFrame(time.Date(2026, time.August, 4, 22, 26, 9, 0, time.Local), config)
	if got := joinedText(elements); got != "10:26PM" {
		t.Fatalf("default clock text = %q, want 10:26PM", got)
	}
}

func TestTwelveHourFrameUsesNativeMeridiem(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.ShowSeconds = true
	now := time.Date(2026, time.August, 4, 13, 5, 9, 0, time.Local)
	elements, _ := clockFrame(now, config)
	if got := joinedText(elements[:5]); got != "01:05:09" {
		t.Fatalf("12-hour clock text = %q, want 01:05:09", got)
	}
	period := elements[len(elements)-1]
	if period.Text != "PM" || period.Font != periodFont || period.Align != "bottom_left" {
		t.Fatalf("meridiem = %#v", period)
	}
	if elements[0].X < 0 || period.X+textWidth(period.Font, period.Text) > displayWidth {
		t.Fatalf("12-hour composition exceeds display: %#v", elements)
	}
}

func TestSecondsCanBeHidden(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.TwelveHour = false
	elements, _ := clockFrame(time.Date(2026, time.August, 4, 23, 58, 59, 0, time.Local), config)
	if got := joinedText(elements); got != "23:58" {
		t.Fatalf("clock text = %q, want 23:58", got)
	}
	if len(elements) != 3 {
		t.Fatalf("clock elements = %d, want 3", len(elements))
	}
}

func TestFadingColonsCompleteSmoothSecondCycleWithoutMovingDigits(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.BlinkColon = true
	base := time.Date(2026, time.August, 4, 10, 26, 9, 0, time.Local)
	bright, _ := clockFrame(base, config)
	dimming, _ := clockFrame(base.Add(250*time.Millisecond), config)
	dim, _ := clockFrame(base.Add(500*time.Millisecond), config)
	brightening, _ := clockFrame(base.Add(750*time.Millisecond), config)
	if len(bright) != len(dim) {
		t.Fatalf("fade element counts = %d/%d", len(bright), len(dim))
	}
	for index := range bright {
		if bright[index].X != dim[index].X || bright[index].Y != dim[index].Y || bright[index].Text != dim[index].Text {
			t.Fatalf("fade moved element %d: %#v -> %#v", index, bright[index], dim[index])
		}
		if bright[index].Text == ":" {
			if bright[index].Color != "#FFFFFFFF" || dimming[index].Color != "#FFFFFFA4" || dim[index].Color != "#FFFFFF48" || brightening[index].Color != "#FFFFFFA4" {
				t.Fatalf("colon fade = %s, %s, %s, %s", bright[index].Color, dimming[index].Color, dim[index].Color, brightening[index].Color)
			}
		}
	}
}

func TestNextDelayUsesOnlyNecessaryRefreshRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 4, 10, 26, 9, int(250*time.Millisecond), time.Local)
	config := DefaultConfig()
	config.BlinkColon = false
	config.ShowSeconds = true
	if got := nextDelay(now, config); got != 750*time.Millisecond {
		t.Fatalf("seconds delay = %s", got)
	}
	config.ShowSeconds = false
	if got := nextDelay(now, config); got != 50*time.Second+750*time.Millisecond {
		t.Fatalf("minute delay = %s", got)
	}
	config.BlinkColon = true
	if got := nextDelay(now, config); got != 30*time.Millisecond {
		t.Fatalf("blink delay = %s", got)
	}
}

func joinedText(elements []barapi.Element) string {
	var result string
	for _, element := range elements {
		result += element.Text
	}
	return result
}

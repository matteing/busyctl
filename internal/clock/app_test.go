package clock

import (
	"testing"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

func TestDefaultFrameMatchesReferenceClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 4, 10, 26, 9, 0, time.Local)
	elements, _ := clockFrame(now, DefaultConfig())
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

func TestTwelveHourFrameUsesNativeMeridiem(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.TwelveHour = true
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
	config.ShowSeconds = false
	elements, _ := clockFrame(time.Date(2026, time.August, 4, 23, 58, 59, 0, time.Local), config)
	if got := joinedText(elements); got != "23:58" {
		t.Fatalf("clock text = %q, want 23:58", got)
	}
	if len(elements) != 3 {
		t.Fatalf("clock elements = %d, want 3", len(elements))
	}
}

func TestBlinkingColonsDoNotMoveDigits(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.BlinkColon = true
	base := time.Date(2026, time.August, 4, 10, 26, 9, int(100*time.Millisecond), time.Local)
	on, _ := clockFrame(base, config)
	off, _ := clockFrame(base.Add(500*time.Millisecond), config)
	if len(on) != len(off) {
		t.Fatalf("blink element counts = %d/%d", len(on), len(off))
	}
	for index := range on {
		if on[index].X != off[index].X || on[index].Y != off[index].Y || on[index].Text != off[index].Text {
			t.Fatalf("blink moved element %d: %#v -> %#v", index, on[index], off[index])
		}
		if on[index].Text == ":" && (on[index].Color != visibleColor || off[index].Color != hiddenColor) {
			t.Fatalf("colon colors = %s/%s", on[index].Color, off[index].Color)
		}
	}
}

func TestNextDelayUsesOnlyNecessaryRefreshRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 4, 10, 26, 9, int(250*time.Millisecond), time.Local)
	config := DefaultConfig()
	if got := nextDelay(now, config); got != 750*time.Millisecond {
		t.Fatalf("seconds delay = %s", got)
	}
	config.ShowSeconds = false
	if got := nextDelay(now, config); got != 50*time.Second+750*time.Millisecond {
		t.Fatalf("minute delay = %s", got)
	}
	config.BlinkColon = true
	if got := nextDelay(now, config); got != 250*time.Millisecond {
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

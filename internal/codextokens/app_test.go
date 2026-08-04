package codextokens

import (
	"context"
	"database/sql"
	"image/png"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

func TestReaderAggregatesThreadsIntoLocalCalendarDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (created_at INTEGER NOT NULL, tokens_used INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("test", -8*60*60)
	now := time.Date(2026, time.August, 4, 14, 0, 0, 0, location)
	yesterday := now.AddDate(0, 0, -1)
	historical := now.AddDate(-2, 0, 0)
	for _, row := range []struct {
		at     time.Time
		tokens int64
	}{
		{now, 125_000}, {now.Add(-time.Hour), 75_000}, {yesterday, 20_000}, {historical, 3_000_000},
	} {
		if _, err := db.Exec(`INSERT INTO threads(created_at, tokens_used) VALUES(?, ?)`, row.at.Unix(), row.tokens); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	previousLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = previousLocal })
	activity, err := openReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer activity.close()
	activity.now = func() time.Time { return now }
	result, err := activity.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Today != 200_000 {
		t.Fatalf("today = %d, want 200000", result.Today)
	}
	if result.Total != 3_220_000 {
		t.Fatalf("total = %d, want 3220000", result.Total)
	}
	if len(result.Days) != calendarWeeks*7 {
		t.Fatalf("calendar contains %d days", len(result.Days))
	}
	foundYesterday := false
	for _, day := range result.Days {
		if day.Date.Format("2006-01-02") == yesterday.Format("2006-01-02") {
			foundYesterday = day.Tokens == 20_000
		}
	}
	if !foundYesterday {
		t.Fatal("yesterday's activity was not aggregated")
	}

	activity.totalOnly = true
	fastResult, err := activity.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fastResult.Total != 3_220_000 || len(fastResult.Days) != 0 || fastResult.Today != 0 {
		t.Fatalf("total-only result = %#v", fastResult)
	}
}

func TestRenderProducesExactDisplaySizedPNG(t *testing.T) {
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	days := make([]dayUsage, calendarWeeks*7)
	for index := range days {
		days[index] = dayUsage{Date: now.AddDate(0, 0, index-len(days)+1), Tokens: int64(index * 10_000)}
	}
	payload, err := render(usage{Days: days, Today: 1_250_000, Total: 4_125_000_000})
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != displayWidth || image.Bounds().Dy() != displayHeight {
		t.Fatalf("image bounds = %v", image.Bounds())
	}
	for y := 0; y < displayHeight; y++ {
		_, _, _, alpha70 := image.At(70, y).RGBA()
		_, _, _, alpha71 := image.At(71, y).RGBA()
		if alpha70 != 0 || alpha71 != 0 {
			t.Fatalf("right padding is occupied at y=%d", y)
		}
	}
}

type displayBar struct {
	drawing barapi.Drawing
	uploads int
	draws   int
}

func (bar *displayBar) UploadAsset(context.Context, string, string, []byte) error {
	bar.uploads++
	return nil
}
func (bar *displayBar) Draw(_ context.Context, drawing barapi.Drawing) error {
	bar.drawing = drawing
	bar.draws++
	return nil
}

type steadyReader struct{ reads chan struct{} }

func (reader *steadyReader) read(context.Context) (usage, error) {
	reader.reads <- struct{}{}
	return usage{Total: 42}, nil
}

func TestFastCountPollingDoesNotRedrawUnchangedTotal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	device := &displayBar{}
	activity := &steadyReader{reads: make(chan struct{}, 8)}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, device, activity, Config{PollInterval: 5 * time.Millisecond, Priority: 100, View: ViewCount})
	}()
	for range 5 {
		select {
		case <-activity.reads:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("fast polling did not continue reading")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if device.draws != 1 || device.uploads != 1 {
		t.Fatalf("unchanged total caused draws=%d uploads=%d", device.draws, device.uploads)
	}
}
func (*displayBar) Clear(context.Context, string) error { return nil }

func TestDisplayUsesBrightNativeTextAtRightEdge(t *testing.T) {
	device := &displayBar{}
	current := usage{Days: make([]dayUsage, calendarWeeks*7), Total: 4_125_000_000}
	if err := display(context.Background(), device, current, Config{Priority: 100, View: ViewGraph}); err != nil {
		t.Fatal(err)
	}
	if device.uploads != 1 || len(device.drawing.Elements) != 2 {
		t.Fatalf("uploads=%d elements=%d", device.uploads, len(device.drawing.Elements))
	}
	text := device.drawing.Elements[1]
	if text.Type != "text" || text.Text != "4.1B" || text.Font != "small" || text.Align != "mid_right" {
		t.Fatalf("text element = %#v", text)
	}
	if text.X != 70 || text.Y != 8 || text.Color != "#E8F7FFFF" {
		t.Fatalf("text placement/color = %#v", text)
	}
}

func TestCountViewIsExactRawTextInDeadCenter(t *testing.T) {
	device := &displayBar{}
	current := usage{Total: 4_125_678_901}
	if err := display(context.Background(), device, current, Config{Priority: 77, View: ViewCount}); err != nil {
		t.Fatal(err)
	}
	if device.uploads != 1 || len(device.drawing.Elements) != 2 {
		t.Fatalf("uploads=%d elements=%d", device.uploads, len(device.drawing.Elements))
	}
	if background := device.drawing.Elements[0]; background.Type != "image" || background.ID != "sparkline" {
		t.Fatalf("background element = %#v", background)
	}
	text := device.drawing.Elements[1]
	if text.Type != "text" || text.Text != "4,125,678,901" || text.Align != "center" {
		t.Fatalf("count element = %#v", text)
	}
	if text.X != displayWidth/2 || text.Y != displayHeight/2 || text.Font != "small" || text.Color != "#FFFFFFFF" {
		t.Fatalf("count placement/font = %#v", text)
	}
	if device.drawing.Priority != 77 {
		t.Fatalf("priority = %d", device.drawing.Priority)
	}
}

func TestTokenTrendShiftsLiveDeltasAcrossDisplay(t *testing.T) {
	var trend tokenTrend
	first := trend.sample(1_000)
	second := trend.sample(1_250)
	third := trend.sample(1_250)
	if len(first) != sparklineSize || first[sparklineSize-1] != 0 {
		t.Fatalf("initial trend = %#v", first)
	}
	if second[sparklineSize-1] != 250 {
		t.Fatalf("second trend tail = %d, want 250", second[sparklineSize-1])
	}
	if third[sparklineSize-2] != 250 || third[sparklineSize-1] != 0 {
		t.Fatalf("shifted trend tail = %v", third[sparklineSize-2:])
	}
}

func TestSparklineIsDisplaySizedPurpleToCyanPNG(t *testing.T) {
	points := make([]int64, sparklineSize)
	points[sparklineSize-4] = 1_000
	points[sparklineSize-2] = 20_000
	payload, err := renderSparkline(points)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != displayWidth || image.Bounds().Dy() != displayHeight {
		t.Fatalf("sparkline bounds = %v", image.Bounds())
	}
	left := sparklineColor(0)
	right := sparklineColor(displayWidth - 1)
	if left.R <= right.R || left.G >= right.G {
		t.Fatalf("gradient endpoints left=%#v right=%#v", left, right)
	}
	_, _, blue, alpha := image.At(displayWidth-2, 10).RGBA()
	if alpha == 0 || blue < 0x5000 {
		t.Fatalf("sparkline interior is not visibly filled: blue=%04x alpha=%04x", blue, alpha)
	}
}

func TestFormattingFitsTinyCounter(t *testing.T) {
	for value, want := range map[int64]string{
		0: "0", 999: "999", 1_250: "1.2K", 12_500: "12K",
		1_250_000: "1.2M", 12_500_000: "12M", 1_250_000_000: "1.2B", 1_250_000_000_000: "1.2T",
	} {
		if got := formatTokens(value); got != want {
			t.Fatalf("formatTokens(%d) = %q, want %q", value, got, want)
		}
	}
}

type changingReader struct{ calls atomic.Int64 }

func (reader *changingReader) read(context.Context) (usage, error) {
	return usage{Days: make([]dayUsage, calendarWeeks*7), Total: reader.calls.Add(1)}, nil
}

type pollingBar struct{ uploads chan struct{} }

func (bar *pollingBar) UploadAsset(context.Context, string, string, []byte) error {
	bar.uploads <- struct{}{}
	return nil
}
func (*pollingBar) Draw(context.Context, barapi.Drawing) error { return nil }
func (*pollingBar) Clear(context.Context, string) error        { return nil }

func TestRunPollsAndRendersChangedAllTimeTotal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	device := &pollingBar{uploads: make(chan struct{}, 4)}
	activity := &changingReader{}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, device, activity, Config{PollInterval: 10 * time.Millisecond, Priority: 100, View: ViewGraph})
	}()
	for range 3 {
		select {
		case <-device.uploads:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("live polling did not upload three changed frames")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls := activity.calls.Load(); calls < 3 {
		t.Fatalf("reader called %d times, want at least 3", calls)
	}
}

func TestConfigRejectsAggressivePolling(t *testing.T) {
	config := DefaultConfig()
	config.PollInterval = 50 * time.Millisecond
	if err := config.Validate(); err == nil {
		t.Fatal("short poll interval unexpectedly valid")
	}
}

func TestDefaultPollingSamplesFiveTimesPerSecond(t *testing.T) {
	if got := DefaultConfig().PollInterval; got != 200*time.Millisecond {
		t.Fatalf("default poll = %s, want 200ms", got)
	}
}

func TestConfigRejectsUnknownView(t *testing.T) {
	config := DefaultConfig()
	config.View = "dashboard"
	if err := config.Validate(); err == nil {
		t.Fatal("unknown view unexpectedly valid")
	}
}

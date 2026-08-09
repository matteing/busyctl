package minecraft

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

type fakeBar struct {
	mu        sync.Mutex
	drawings  []barapi.Drawing
	failDraws int
	drawn     chan barapi.Drawing
}

func (*fakeBar) UploadAsset(context.Context, string, string, []byte) error { return nil }
func (*fakeBar) Clear(context.Context, string) error                       { return nil }
func (fake *fakeBar) Draw(_ context.Context, drawing barapi.Drawing) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.failDraws > 0 {
		fake.failDraws--
		return errors.New("transient draw failure")
	}
	fake.drawings = append(fake.drawings, drawing)
	if fake.drawn != nil {
		select {
		case fake.drawn <- drawing:
		default:
		}
	}
	return nil
}

func TestDefaultConfigTargetsMatteingServer(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "")
	t.Setenv("BUSYBAR_TOKEN", "")
	t.Setenv("MINECRAFT_SERVER", "")
	config := DefaultConfig()
	if config.Server != "mc.matteing.com" || config.PollInterval != 10*time.Second || config.Priority != 100 {
		t.Fatalf("default config = %#v", config)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	valid := Config{Server: "mc.matteing.com", Priority: 100, PollInterval: time.Second}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"empty server": func(config *Config) { config.Server = "" },
		"bad port":     func(config *Config) { config.Server = "example.com:70000" },
		"priority":     func(config *Config) { config.Priority = 0 },
		"fast poll":    func(config *Config) { config.PollInterval = 500 * time.Millisecond },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatalf("invalid config accepted: %#v", config)
			}
		})
	}
}

func TestGeneratedAssetsAreExpectedPNGDimensions(t *testing.T) {
	t.Parallel()
	assets := minecraftAssets()
	if len(assets) != logoFrameCount+4 {
		t.Fatalf("assets = %d, want %d", len(assets), logoFrameCount+4)
	}
	for index, asset := range assets {
		config, err := png.DecodeConfig(bytes.NewReader(asset.payload))
		if err != nil {
			t.Fatalf("decode %s: %v", asset.path, err)
		}
		want := iconSize
		if index < logoFrameCount {
			want = logoSize
		}
		if config.Width != want || config.Height != want {
			t.Fatalf("%s dimensions = %dx%d, want %dx%d", asset.path, config.Width, config.Height, want, want)
		}
	}
}

func TestCreeperSparkleAnimationIsSmoothAndKeepsFaceCrisp(t *testing.T) {
	t.Parallel()
	frames := []int{0, 1, logoFrameCount / 2}
	images := make([]interface{ At(int, int) color.Color }, len(frames))
	for index, frame := range frames {
		decoded, err := png.Decode(bytes.NewReader(creeperLogoPNG(frame)))
		if err != nil {
			t.Fatal(err)
		}
		images[index] = decoded
	}
	changed := 0
	maximumDelta := 0
	for y := 0; y < logoSize; y++ {
		for x := 0; x < logoSize; x++ {
			first := color.NRGBAModel.Convert(images[0].At(x, y)).(color.NRGBA)
			second := color.NRGBAModel.Convert(images[1].At(x, y)).(color.NRGBA)
			for _, delta := range []int{abs(int(first.R) - int(second.R)), abs(int(first.G) - int(second.G)), abs(int(first.B) - int(second.B))} {
				if delta > maximumDelta {
					maximumDelta = delta
				}
			}
			if first != second {
				changed++
			}
		}
	}
	if changed == 0 || maximumDelta > 24 {
		t.Fatalf("adjacent sparkle frames changed %d pixels with max channel delta %d", changed, maximumDelta)
	}
	if bytes.Equal(creeperLogoPNG(0), creeperLogoPNG(logoFrameCount/2)) {
		t.Fatal("opposite sparkle phases are identical")
	}
	for _, point := range [][2]int{{3, 4}, {6, 7}, {5, 9}} {
		first := color.NRGBAModel.Convert(images[0].At(point[0], point[1])).(color.NRGBA)
		last := color.NRGBAModel.Convert(images[2].At(point[0], point[1])).(color.NRGBA)
		if first != last {
			t.Fatalf("face pixel %v animated from %#v to %#v", point, first, last)
		}
	}
}

func TestOnlineButtonUsesGlossyGradientInsteadOfCheckmark(t *testing.T) {
	t.Parallel()
	decoded, err := png.Decode(bytes.NewReader(statusPNG(true)))
	if err != nil {
		t.Fatal(err)
	}
	topLeft := color.NRGBAModel.Convert(decoded.At(1, 1)).(color.NRGBA)
	bottomRight := color.NRGBAModel.Convert(decoded.At(3, 3)).(color.NRGBA)
	brightness := func(pixel color.NRGBA) int { return int(pixel.R) + int(pixel.G) + int(pixel.B) }
	if brightness(topLeft) <= brightness(bottomRight) {
		t.Fatalf("online button lighting is not top-left to bottom-right: %#v vs %#v", topLeft, bottomRight)
	}
	unique := make(map[color.NRGBA]struct{})
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if pixel.A != 0 {
				unique[pixel] = struct{}{}
			}
			if pixel == (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
				t.Fatal("online button still contains a white checkmark pixel")
			}
		}
	}
	if len(unique) < 12 {
		t.Fatalf("online button has only %d opaque colors; want a richer gradient", len(unique))
	}
}

func TestOnlineFrameUsesTwoPixelLogoGap(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, priority: 42}
	if err := app.draw(context.Background(), displaySnapshot{Mode: modeOnline, PlayersOnline: 2, PlayersMax: 5}); err != nil {
		t.Fatal(err)
	}
	drawing := device.drawings[0]
	if drawing.ApplicationName != ApplicationID || drawing.Priority != 42 || len(drawing.Elements) != 5 {
		t.Fatalf("drawing = %#v", drawing)
	}
	logo, statusIcon, statusText, playerIcon, playerText := drawing.Elements[0], drawing.Elements[1], drawing.Elements[2], drawing.Elements[3], drawing.Elements[4]
	if logo.Path != logoPath(0) || logo.X != 1 || logo.Y != 1 {
		t.Fatalf("logo = %#v", logo)
	}
	if contentX-(logoX+logoSize) != 2 {
		t.Fatalf("logo/content gap = %d, want 2", contentX-(logoX+logoSize))
	}
	if statusIcon.Path != onlinePath || statusIcon.X != 17 || statusIcon.Y != 2 {
		t.Fatalf("status icon = %#v", statusIcon)
	}
	if statusText.Text != "ONLINE" || statusText.Color != statusOnlineColor || statusText.X != 23 || statusText.Y != 0 || statusText.Width != 48 {
		t.Fatalf("status text = %#v", statusText)
	}
	if playerIcon.Path != playersPath || playerIcon.X != 17 || playerIcon.Y != 9 {
		t.Fatalf("players icon = %#v", playerIcon)
	}
	if playerText.Text != "2 / 5" || playerText.X != 23 || playerText.Y != 7 || playerText.Width != 48 {
		t.Fatalf("players text = %#v", playerText)
	}
}

func TestOfflineFrameUsesRedStatusIcon(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, priority: 100}
	if err := app.draw(context.Background(), displaySnapshot{Mode: modeOffline}); err != nil {
		t.Fatal(err)
	}
	elements := device.drawings[0].Elements
	if elements[1].Path != offlinePath || elements[2].Text != "OFFLINE" || elements[2].Color != statusOfflineColor || elements[4].Text != "-- / --" {
		t.Fatalf("offline elements = %#v", elements)
	}
}

func TestFrameRefreshesAndFailedFrameRetries(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, priority: 100}
	snapshot := displaySnapshot{Mode: modeOnline, PlayersOnline: 2, PlayersMax: 5}
	if err := app.draw(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := app.draw(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(device.drawings) != 2 {
		t.Fatalf("periodic refresh drew %d times, want 2", len(device.drawings))
	}
	device.failDraws = 1
	changed := displaySnapshot{Mode: modeOnline, PlayersOnline: 3, PlayersMax: 5}
	if err := app.draw(context.Background(), changed); err == nil {
		t.Fatal("failed draw unexpectedly succeeded")
	}
	if err := app.draw(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if len(device.drawings) != 3 {
		t.Fatalf("retried state produced %d successful draws", len(device.drawings))
	}
}

type queuedFetcher struct {
	result pollResult
}

func (fetcher queuedFetcher) Fetch(context.Context, string) (Status, error) {
	return fetcher.result.status, fetcher.result.err
}

func TestRunDrawsCheckingThenLiveStatusAndStopsOnCancel(t *testing.T) {
	device := &fakeBar{drawn: make(chan barapi.Drawing, 2)}
	app := application{
		bar: device, fetcher: queuedFetcher{result: pollResult{status: Status{Online: true, PlayersOnline: 2, PlayersMax: 5}}},
		server: "mc.matteing.com", priority: 100, pollInterval: time.Hour, sparkleEvery: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.run(ctx) }()

	for index := 0; index < 2; index++ {
		select {
		case <-device.drawn:
		case <-time.After(time.Second):
			cancel()
			t.Fatalf("timed out waiting for draw %d", index+1)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("app did not stop after cancellation")
	}
	device.mu.Lock()
	defer device.mu.Unlock()
	if device.drawings[0].Elements[2].Text != "CHECKING" || device.drawings[1].Elements[2].Text != "ONLINE" {
		t.Fatalf("draw sequence = %#v", device.drawings)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

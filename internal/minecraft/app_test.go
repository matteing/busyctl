package minecraft

import (
	"bytes"
	"context"
	"errors"
	"image"
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

func TestDefaultConfigTargetsHomelabServer(t *testing.T) {
	t.Setenv("BUSYBAR_HOST", "")
	t.Setenv("BUSYBAR_TOKEN", "")
	t.Setenv("MINECRAFT_SERVER", "")
	config := DefaultConfig()
	if config.Server != "homelab" || config.PollInterval != 10*time.Second || config.Priority != 100 {
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
	if len(assets) != 2*logoFrameCount+4 {
		t.Fatalf("assets = %d, want %d", len(assets), 2*logoFrameCount+4)
	}
	for index, asset := range assets {
		config, err := png.DecodeConfig(bytes.NewReader(asset.payload))
		if err != nil {
			t.Fatalf("decode %s: %v", asset.path, err)
		}
		want := iconSize
		if index < 2*logoFrameCount {
			want = logoSize
		}
		if config.Width != want || config.Height != want {
			t.Fatalf("%s dimensions = %dx%d, want %dx%d", asset.path, config.Width, config.Height, want, want)
		}
	}
}

func TestOfflineCreeperFramesAreGrayscaleAndStillAnimated(t *testing.T) {
	t.Parallel()
	colorfulPixelFound := false
	for frame := 0; frame < logoFrameCount; frame++ {
		colorFrame, err := png.Decode(bytes.NewReader(creeperLogoPNG(frame)))
		if err != nil {
			t.Fatal(err)
		}
		grayFrame, err := png.Decode(bytes.NewReader(offlineCreeperLogoPNG(frame)))
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < logoSize; y++ {
			for x := 0; x < logoSize; x++ {
				colorPixel := color.NRGBAModel.Convert(colorFrame.At(x, y)).(color.NRGBA)
				grayPixel := color.NRGBAModel.Convert(grayFrame.At(x, y)).(color.NRGBA)
				if colorPixel.A != grayPixel.A {
					t.Fatalf("frame %d pixel (%d, %d) alpha = %d, want %d", frame, x, y, grayPixel.A, colorPixel.A)
				}
				if grayPixel.A != 0 && (grayPixel.R != grayPixel.G || grayPixel.G != grayPixel.B) {
					t.Fatalf("frame %d pixel (%d, %d) is not grayscale: %#v", frame, x, y, grayPixel)
				}
				if colorPixel.A != 0 && (colorPixel.R != colorPixel.G || colorPixel.G != colorPixel.B) {
					colorfulPixelFound = true
				}
			}
		}
	}
	if !colorfulPixelFound {
		t.Fatal("color creeper frames unexpectedly contain no hue")
	}
	if bytes.Equal(offlineCreeperLogoPNG(0), offlineCreeperLogoPNG(logoFrameCount/2)) {
		t.Fatal("offline creeper animation is frozen")
	}
}

func TestCreeperSparkleAnimationIsSmoothAndKeepsFaceCrisp(t *testing.T) {
	t.Parallel()
	images := make([]image.Image, logoFrameCount)
	for frame := 0; frame < logoFrameCount; frame++ {
		decoded, err := png.Decode(bytes.NewReader(creeperLogoPNG(frame)))
		if err != nil {
			t.Fatal(err)
		}
		images[frame] = decoded
	}
	changed := 0
	maximumDelta := 0
	for frame := 0; frame < logoFrameCount; frame++ {
		next := (frame + 1) % logoFrameCount
		for y := 0; y < logoSize; y++ {
			for x := 0; x < logoSize; x++ {
				first := color.NRGBAModel.Convert(images[frame].At(x, y)).(color.NRGBA)
				second := color.NRGBAModel.Convert(images[next].At(x, y)).(color.NRGBA)
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
	}
	if changed == 0 || maximumDelta > 48 {
		t.Fatalf("adjacent sparkle frames changed %d pixels with max channel delta %d", changed, maximumDelta)
	}
	if bytes.Equal(creeperLogoPNG(0), creeperLogoPNG(logoFrameCount/2)) {
		t.Fatal("opposite sparkle phases are identical")
	}
	for y := 0; y < logoSize; y++ {
		for x := 0; x < logoSize; x++ {
			if !creeperFacePixel(x, y) {
				continue
			}
			first := color.NRGBAModel.Convert(images[0].At(x, y)).(color.NRGBA)
			for frame := 1; frame < logoFrameCount; frame++ {
				current := color.NRGBAModel.Convert(images[frame].At(x, y)).(color.NRGBA)
				if first != current {
					t.Fatalf("face pixel (%d, %d) animated from %#v to %#v in frame %d", x, y, first, current, frame)
				}
			}
		}
	}
}

func TestCreeperSparklesUseScatteredSchedule(t *testing.T) {
	t.Parallel()
	if len(creeperSparkles) != 9 {
		t.Fatalf("sparkle events = %d, want 9", len(creeperSparkles))
	}
	locations := make(map[[2]int]struct{}, len(creeperSparkles))
	for _, event := range creeperSparkles {
		location := [2]int{event.x, event.y}
		if _, duplicate := locations[location]; duplicate {
			t.Fatalf("duplicate sparkle location %v", location)
		}
		locations[location] = struct{}{}
		if !sparkleFits(event.x, event.y) {
			t.Fatalf("sparkle at %v overlaps the creeper face", location)
		}
		if event.phase < 0 || event.phase >= 1 || event.radius < 0.14 || event.radius > 0.19 || event.strength < 0.78 || event.strength > 1 {
			t.Fatalf("invalid sparkle event: %#v", event)
		}
	}
}

func TestCreeperWaveHasProminentContrast(t *testing.T) {
	t.Parallel()
	frames := make([]image.Image, logoFrameCount)
	for frame := 0; frame < logoFrameCount; frame++ {
		decoded, err := png.Decode(bytes.NewReader(creeperLogoPNG(frame)))
		if err != nil {
			t.Fatal(err)
		}
		frames[frame] = decoded
	}

	maximumBrightnessSpan := 0
	for y := 0; y < logoSize; y++ {
		for x := 0; x < logoSize; x++ {
			if creeperFacePixel(x, y) || sparkleTouchesPixel(x, y) {
				continue
			}
			minimum, maximum := 3*255, 0
			for _, frame := range frames {
				pixel := color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
				brightness := int(pixel.R) + int(pixel.G) + int(pixel.B)
				if brightness < minimum {
					minimum = brightness
				}
				if brightness > maximum {
					maximum = brightness
				}
			}
			if span := maximum - minimum; span > maximumBrightnessSpan {
				maximumBrightnessSpan = span
			}
		}
	}
	if maximumBrightnessSpan < 100 {
		t.Fatalf("wave brightness span = %d, want at least 100", maximumBrightnessSpan)
	}
	if loop := logoFrameCount * logoFrameInterval; loop > 3500*time.Millisecond {
		t.Fatalf("wave loop = %s, want no slower than 3.5s", loop)
	}
}

func sparkleTouchesPixel(x, y int) bool {
	for _, event := range creeperSparkles {
		for _, point := range [][2]int{{event.x, event.y}, {event.x - 1, event.y}, {event.x + 1, event.y}, {event.x, event.y - 1}, {event.x, event.y + 1}} {
			if point == [2]int{x, y} {
				return true
			}
		}
	}
	return false
}

func TestStatusButtonsUseMatchingGlossyGradientsWithoutMarks(t *testing.T) {
	t.Parallel()
	online, err := png.Decode(bytes.NewReader(statusPNG(true)))
	if err != nil {
		t.Fatal(err)
	}
	offline, err := png.Decode(bytes.NewReader(statusPNG(false)))
	if err != nil {
		t.Fatal(err)
	}
	brightness := func(pixel color.NRGBA) int { return int(pixel.R) + int(pixel.G) + int(pixel.B) }
	for name, decoded := range map[string]image.Image{"online": online, "offline": offline} {
		topLeft := color.NRGBAModel.Convert(decoded.At(1, 1)).(color.NRGBA)
		bottomRight := color.NRGBAModel.Convert(decoded.At(3, 3)).(color.NRGBA)
		if brightness(topLeft) <= brightness(bottomRight) {
			t.Fatalf("%s button lighting is not top-left to bottom-right: %#v vs %#v", name, topLeft, bottomRight)
		}
		unique := make(map[color.NRGBA]struct{})
		for y := 0; y < iconSize; y++ {
			for x := 0; x < iconSize; x++ {
				pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
				if pixel.A != 0 {
					unique[pixel] = struct{}{}
				}
				if pixel == (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
					t.Fatalf("%s button contains a white symbol pixel", name)
				}
			}
		}
		if len(unique) < 12 {
			t.Fatalf("%s button has only %d opaque colors; want a richer gradient", name, len(unique))
		}
	}
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			green := color.NRGBAModel.Convert(online.At(x, y)).(color.NRGBA)
			red := color.NRGBAModel.Convert(offline.At(x, y)).(color.NRGBA)
			if green.A != red.A || green.R != red.G || green.G != red.R || green.B != red.B {
				t.Fatalf("button pixel (%d, %d) does not share lighting: green=%#v red=%#v", x, y, green, red)
			}
		}
	}
	greenCenter := color.NRGBAModel.Convert(online.At(2, 2)).(color.NRGBA)
	redCenter := color.NRGBAModel.Convert(offline.At(2, 2)).(color.NRGBA)
	if greenCenter.G <= greenCenter.R || redCenter.R <= redCenter.G {
		t.Fatalf("status hues are not dominant at center: green=%#v red=%#v", greenCenter, redCenter)
	}
}

func TestPlayerIconUsesDimSecondaryPalette(t *testing.T) {
	t.Parallel()
	players, err := png.Decode(bytes.NewReader(playersPNG()))
	if err != nil {
		t.Fatal(err)
	}
	status, err := png.Decode(bytes.NewReader(statusPNG(true)))
	if err != nil {
		t.Fatal(err)
	}
	brightness := func(pixel color.NRGBA) int { return int(pixel.R) + int(pixel.G) + int(pixel.B) }
	playerLight := color.NRGBAModel.Convert(players.At(2, 0)).(color.NRGBA)
	playerShade := color.NRGBAModel.Convert(players.At(2, 4)).(color.NRGBA)
	statusHighlight := color.NRGBAModel.Convert(status.At(1, 1)).(color.NRGBA)
	if brightness(playerLight) >= brightness(statusHighlight) || brightness(playerShade) >= brightness(playerLight) {
		t.Fatalf("player hierarchy is not dimmer than status: light=%#v shade=%#v status=%#v", playerLight, playerShade, statusHighlight)
	}
	if brightness(playerLight) < 400 {
		t.Fatalf("player icon is too dim to read: %#v", playerLight)
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
	if playerText.Text != "2 / 5" || playerText.Color != playersColor || playerText.X != 23 || playerText.Y != 7 || playerText.Width != 48 {
		t.Fatalf("players text = %#v", playerText)
	}
}

func TestOfflineFrameUsesGrayscaleLogoAndRedStatusLamp(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, priority: 100}
	if err := app.draw(context.Background(), displaySnapshot{Mode: modeOffline}); err != nil {
		t.Fatal(err)
	}
	elements := device.drawings[0].Elements
	if elements[0].Path != offlineLogoPath(0) || elements[1].Path != offlinePath || elements[2].Text != "OFFLINE" || elements[2].Color != statusOfflineColor || elements[4].Text != "-- / --" || elements[4].Color != playersColor {
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
	if device.drawings[0].Elements[0].Path != logoPath(0) || device.drawings[0].Elements[2].Text != "CHECKING" || device.drawings[1].Elements[2].Text != "ONLINE" {
		t.Fatalf("draw sequence = %#v", device.drawings)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

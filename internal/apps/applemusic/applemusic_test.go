package applemusic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	barapi "github.com/matteing/busybar-apps/internal/busybar"
	"github.com/matteing/busybar-apps/internal/media"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeBar struct {
	drawings []barapi.DisplayElements
	uploads  [][]byte
	clears   int
}

func (f *fakeBar) UploadAsset(_ context.Context, _, _ string, payload []byte) error {
	f.uploads = append(f.uploads, payload)
	return nil
}

func (f *fakeBar) DeleteAssets(_ context.Context, _ string) error { return nil }

func (f *fakeBar) Draw(_ context.Context, drawing barapi.DisplayElements) error {
	f.drawings = append(f.drawings, drawing)
	return nil
}

func (f *fakeBar) Clear(_ context.Context, _ string) error {
	f.clears++
	return nil
}

func TestTrackNormalize(t *testing.T) {
	t.Parallel()
	track := Track{Title: "  Song   Name ", ArtistName: " The Artist "}
	track.normalize()
	if track.Name != "Song Name" || track.Artist != "The Artist" {
		t.Fatalf("unexpected normalized track: %#v", track)
	}
}

func TestCleanTextTransliteratesForBundledFont(t *testing.T) {
	t.Parallel()
	got := cleanText("Beyoncé — déjà vu… 👀🎵 東京")
	want := "Beyonce - deja vu... ? ?"
	if got != want {
		t.Fatalf("cleanText() = %q, want %q", got, want)
	}
}

func TestTrackCoverUsesNestedAlbumArtwork(t *testing.T) {
	t.Parallel()
	track := Track{Album: &Album{CoverURL: "https://example.test/album.jpg"}}
	if got := track.cover(); got != "https://example.test/album.jpg" {
		t.Fatalf("cover() = %q", got)
	}
}

func TestSourceFetch(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"track":{"id":"1","name":"Song","artist":"Artist","coverUrl":"https://example.test/cover.jpg"},
				"isPlaying":true,
				"recentAlbums":[]
			}`)),
			Request: request,
		}, nil
	})
	source := sourceClient{
		url:  "https://example.test/now-playing",
		http: &http.Client{Transport: transport},
	}
	result, err := source.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Track == nil || result.Track.Name != "Song" || !result.IsPlaying {
		t.Fatalf("unexpected now-playing response: %#v", result)
	}
}

func TestRenderCoordinatesScrollingRows(t *testing.T) {
	t.Parallel()
	bar := &fakeBar{}
	app := application{
		options: options{priority: 100, scrollRate: 1500, scrollTime: 6 * time.Second, scrollRest: 3 * time.Second},
		bar:     bar,
		track:   &Track{Name: "A long song title", Artist: "A long artist name"},
	}
	wantRates := [][2]int{{1500, 0}, {0, 0}, {0, 1500}, {0, 0}}
	for phase, want := range wantRates {
		app.phase = scrollPhase(phase)
		app.lastDrawing = ""
		if err := app.render(context.Background()); err != nil {
			t.Fatal(err)
		}
		elements := bar.drawings[len(bar.drawings)-1].Elements
		if len(elements) != 2 {
			t.Fatalf("phase %d element count = %d", phase, len(elements))
		}
		title, artist := elements[0], elements[1]
		if title.ScrollRate == nil || artist.ScrollRate == nil {
			t.Fatalf("phase %d did not explicitly set both scroll rates", phase)
		}
		got := [2]int{*title.ScrollRate, *artist.ScrollRate}
		if got != want {
			t.Fatalf("phase %d scroll rates = %v, want %v", phase, got, want)
		}
		if title.Font != "small" || artist.Font != "small" || title.Width != displayWidth-2*viewPadding {
			t.Fatalf("unexpected bundled typography: %#v", elements)
		}
	}
	if len(bar.uploads) != 0 {
		t.Fatalf("text scene uploaded %d unnecessary waveform assets", len(bar.uploads))
	}
	app.view = viewWaveform
	app.lastDrawing = ""
	if err := app.render(context.Background()); err != nil {
		t.Fatal(err)
	}
	elements := bar.drawings[len(bar.drawings)-1].Elements
	if len(elements) != 1 || elements[0].Type != "image" || elements[0].X != viewPadding {
		t.Fatalf("waveform scene elements = %#v", elements)
	}
	if bar.clears != 1 {
		t.Fatalf("scene switch clears = %d, want 1", bar.clears)
	}
	if len(bar.uploads) != media.WaveformFrameCount {
		t.Fatalf("waveform uploads = %d, want %d", len(bar.uploads), media.WaveformFrameCount)
	}
}

func TestCarouselDurations(t *testing.T) {
	t.Parallel()
	app := application{
		options: options{scrollTime: 6 * time.Second, scrollRest: 3 * time.Second},
		track:   &Track{Name: "Song"},
	}
	want := []time.Duration{6 * time.Second, 3 * time.Second, 6 * time.Second, 3 * time.Second}
	for phase, duration := range want {
		app.phase = scrollPhase(phase)
		if got := app.phaseDuration(); got != duration {
			t.Fatalf("phase %d duration = %s, want %s", phase, got, duration)
		}
	}
	app.view = viewWaveform
	if got := app.phaseDuration(); got != waveformFrameTime {
		t.Fatalf("waveform duration = %s", got)
	}
}

func TestShortTextDoesNotScroll(t *testing.T) {
	t.Parallel()
	if shouldScroll("Latch", 52) {
		t.Fatal("short title should remain stationary")
	}
	if !shouldScroll("Disclosure & Sam Smith", 52) {
		t.Fatal("long artist should scroll")
	}
}

func TestDialSelectsPersistentPlayingView(t *testing.T) {
	t.Parallel()
	app := application{phase: phaseTitle}
	for range 12 {
		app.advancePlaying()
	}
	if app.view != viewText {
		t.Fatal("text cycle changed the selected view")
	}
	app.rotateView(1)
	if app.view != viewWaveform {
		t.Fatal("first dial input did not select waveform")
	}
	for range 120 {
		app.advancePlaying()
		if app.view != viewWaveform {
			t.Fatal("waveform selection changed without dial input")
		}
	}
	app.rotateView(-1)
	if app.view != viewText || app.phase != phaseTitle {
		t.Fatalf("second dial input = view %d phase %d", app.view, app.phase)
	}
}

func TestMultiStepDialDeltaStillSwitchesOnce(t *testing.T) {
	t.Parallel()
	app := application{view: viewText}
	app.rotateView(-4)
	if app.view != viewWaveform {
		t.Fatalf("view after multi-step delta = %d", app.view)
	}
}

func TestRecentCarouselDuration(t *testing.T) {
	t.Parallel()
	app := application{options: options{recentSpeed: 50 * time.Millisecond}}
	if got := app.phaseDuration(); got != animationFrameTime {
		t.Fatalf("recent duration = %s", got)
	}
}

func TestRecentCarouselWrapsWithoutAResetFrame(t *testing.T) {
	t.Parallel()
	bar := &fakeBar{}
	recent := []Track{
		{ID: "1", Name: "One", CoverURL: "https://example.test/1.jpg"},
		{ID: "2", Name: "Two", CoverURL: "https://example.test/2.jpg"},
	}
	app := application{
		options:          options{priority: 100, recentSpeed: animationFrameTime},
		bar:              bar,
		recent:           recent,
		recentArt:        recentFingerprint(recent),
		recentStripWidth: 80,
		recentTileWidths: []int{72, 8},
		recentX:          79,
	}
	if err := app.render(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.advanceRecent()
	if app.recentX != 0 {
		t.Fatalf("wrapped offset = %f", app.recentX)
	}
	if err := app.render(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(bar.drawings) != 2 {
		t.Fatalf("draw count = %d", len(bar.drawings))
	}
	for _, drawing := range bar.drawings {
		if len(drawing.Elements) != 2 {
			t.Fatalf("carousel element count = %d", len(drawing.Elements))
		}
	}
}

func TestRecentCarouselInterpolatesAtSixtyFPS(t *testing.T) {
	t.Parallel()
	bar := &fakeBar{}
	recent := []Track{{ID: "1", Name: "One", CoverURL: "https://example.test/1.jpg"}}
	app := application{
		options:          options{priority: 100, recentSpeed: 50 * time.Millisecond},
		bar:              bar,
		recent:           recent,
		recentArt:        recentFingerprint(recent),
		recentStripWidth: 80,
		recentTileWidths: []int{72, 8},
	}
	app.advanceRecent()
	if app.recentX <= 0 || app.recentX >= 1 {
		t.Fatalf("subpixel offset = %f", app.recentX)
	}
	if err := app.render(context.Background()); err != nil {
		t.Fatal(err)
	}
	elements := bar.drawings[0].Elements
	if len(elements) != 2 || elements[0].Path == "recent-strip-00-00.png" {
		t.Fatalf("subpixel strip elements = %#v", elements)
	}
}

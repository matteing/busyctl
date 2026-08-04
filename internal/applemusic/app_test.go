package applemusic

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

type fakeBar struct {
	drawings  []barapi.Drawing
	clears    int
	drawCalls int
	failDraws int
	uploads   []string
}

func (f *fakeBar) UploadAsset(_ context.Context, _ string, name string, _ []byte) error {
	f.uploads = append(f.uploads, name)
	return nil
}
func (f *fakeBar) Draw(_ context.Context, drawing barapi.Drawing) error {
	f.drawCalls++
	if f.failDraws > 0 {
		f.failDraws--
		return errors.New("transient draw failure")
	}
	f.drawings = append(f.drawings, drawing)
	return nil
}
func (f *fakeBar) Clear(context.Context, string) error { f.clears++; return nil }

func TestConfigViews(t *testing.T) {
	t.Parallel()
	for _, view := range []string{ViewTitles, ViewVisualizer} {
		if err := (Config{View: view}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", view, err)
		}
	}
	if err := (Config{View: "both"}).Validate(); err == nil {
		t.Fatal("invalid view was accepted")
	}
}

func TestSequentialTextScrolling(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{
		bar: device, options: options{priority: 100}, playing: true,
		track: &Track{Name: "A sufficiently long song title", Artist: "A sufficiently long artist name"},
	}
	want := [][2]int{{defaultScrollRate, 0}, {0, 0}, {0, defaultScrollRate}, {0, 0}}
	for phase, rates := range want {
		app.phase = textPhase(phase)
		if err := app.drawText(context.Background(), false, false); err != nil {
			t.Fatal(err)
		}
		elements := device.drawings[len(device.drawings)-1].Elements
		if len(elements) != 2 || elements[0].ScrollRate == nil || elements[1].ScrollRate == nil {
			t.Fatalf("phase %d elements = %#v", phase, elements)
		}
		got := [2]int{*elements[0].ScrollRate, *elements[1].ScrollRate}
		if got != rates {
			t.Fatalf("phase %d rates = %v, want %v", phase, got, rates)
		}
	}
}

func TestPausedTextIsGrayAndStationary(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, track: &Track{Name: "Song", Artist: "Artist"}, phase: phaseTitle}
	if err := app.drawText(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	elements := device.drawings[0].Elements
	if len(elements) != 3 || elements[0].Path != grayCoverPath(0) {
		t.Fatalf("paused elements = %#v", elements)
	}
	if *elements[1].ScrollRate != 0 || *elements[2].ScrollRate != 0 {
		t.Fatal("paused text is still scrolling")
	}
	if elements[1].Color != "#777777FF" || elements[2].Color != "#505050FF" {
		t.Fatal("paused text is not gray")
	}
}

func TestVisualizerTickMutatesOneElement(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{bar: device, options: options{priority: 100}, visualFrame: 7}
	if err := app.drawVisualizerFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	elements := device.drawings[0].Elements
	if len(elements) != 1 || elements[0].ID != "visualizer" || elements[0].Path != visualizerPath(0, 7) {
		t.Fatalf("visualizer tick = %#v", elements)
	}
	if elements[0].X != contentX || elements[0].Y != contentY {
		t.Fatalf("visualizer position = (%d,%d), want (%d,%d)", elements[0].X, elements[0].Y, contentX, contentY)
	}
}

func TestOnePixelOuterLayout(t *testing.T) {
	t.Parallel()
	device := &fakeBar{}
	app := application{
		bar: device, options: options{priority: 100},
		track: &Track{Name: "Song", Artist: "Artist"},
	}
	if err := app.drawText(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	elements := device.drawings[0].Elements
	if len(elements) != 3 {
		t.Fatalf("text composition = %#v", elements)
	}
	cover, title, artist := elements[0], elements[1], elements[2]
	if cover.X != 1 || cover.Y != 1 {
		t.Fatalf("cover position = (%d,%d), want (1,1)", cover.X, cover.Y)
	}
	if artworkSize != 14 {
		t.Fatalf("artwork size = %d, want 14", artworkSize)
	}
	if title.X != 16 || title.Y != 1 || title.Width != 55 {
		t.Fatalf("title bounds = x%d y%d w%d, want x16 y1 w55", title.X, title.Y, title.Width)
	}
	if artist.X != 16 || artist.Y != 8 || artist.Width != 55 {
		t.Fatalf("artist bounds = x%d y%d w%d, want x16 y8 w55", artist.X, artist.Y, artist.Width)
	}
}

func TestFailedVisualizerDrawRetriesFullComposition(t *testing.T) {
	device := &fakeBar{failDraws: 1}
	app := application{
		bar: device, options: options{priority: 100}, playing: true,
		track: &Track{Name: "Song", Artist: "Artist"}, scene: sceneText,
		visualFrames: [][]byte{{1}}, assetsReady: true, renderDirty: true,
	}
	app.scene = sceneVisualizer
	if err := app.reconcile(context.Background()); err == nil {
		t.Fatal("failed visualizer draw unexpectedly succeeded")
	}
	if !app.renderDirty || app.scene != sceneVisualizer {
		t.Fatalf("failed draw state: dirty=%v scene=%d", app.renderDirty, app.scene)
	}
	if err := app.animationStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if app.renderDirty || device.clears != 0 || device.drawCalls != 2 {
		t.Fatalf("retry state: dirty=%v clears=%d draws=%d", app.renderDirty, device.clears, device.drawCalls)
	}
	elements := device.drawings[0].Elements
	if len(elements) != 2 || elements[0].ID != "cover" || elements[1].ID != "visualizer" {
		t.Fatalf("retry was not a full visualizer composition: %#v", elements)
	}
}

func TestPausedSnapshotFreezesCurrentTrack(t *testing.T) {
	device := &fakeBar{}
	current := &Track{ID: "current", Name: "Current Song", Artist: "Current Artist"}
	app := application{
		bar: device, options: options{priority: 100}, playing: true,
		track: current, trackKey: fingerprint(current), assetsReady: true,
	}
	queued := &Track{ID: "queued", Name: "Queued Song", Artist: "Queued Artist"}
	if err := app.applySnapshot(context.Background(), snapshot{Track: queued, IsPlaying: false}); err != nil {
		t.Fatal(err)
	}
	if app.track != current || app.trackKey != fingerprint(current) {
		t.Fatalf("paused snapshot replaced frozen track: %#v", app.track)
	}
	if app.playing || app.renderDirty {
		t.Fatalf("paused state did not reconcile: playing=%v dirty=%v", app.playing, app.renderDirty)
	}
	elements := device.drawings[0].Elements
	if len(elements) != 3 || elements[0].Path != grayCoverPath(0) || elements[1].Text != current.Name {
		t.Fatalf("paused composition = %#v", elements)
	}
	if device.clears != 0 {
		t.Fatalf("paused transition cleared the current scene %d times", device.clears)
	}
}

func TestAssetBankPathsAreBoundedAndDistinct(t *testing.T) {
	t.Parallel()
	for bank := range assetBankCount {
		if colorCoverPath(bank) == grayCoverPath(bank) {
			t.Fatalf("bank %d cover paths collide", bank)
		}
		if visualizerPath(bank, 7) == pausedVisualizerPath(bank) {
			t.Fatalf("bank %d visualizer paths collide", bank)
		}
	}
	if visualizerPath(0, 7) == visualizerPath(1, 7) || colorCoverPath(0) == colorCoverPath(1) {
		t.Fatal("active and inactive asset banks collide")
	}
}

func TestPrepareTrackStagesInactiveBankWithoutClearing(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			source.SetNRGBA(x, y, color.NRGBA{R: 220, G: 45, B: 130, A: 255})
		}
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, source); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write(payload.Bytes())
	}))
	defer server.Close()

	device := &fakeBar{}
	app := application{bar: device, scene: sceneText, assetBank: 0}
	track := &Track{CoverURL: server.URL}
	if err := app.prepareTrack(context.Background(), track); err != nil {
		t.Fatal(err)
	}
	if device.clears != 0 {
		t.Fatalf("track preparation cleared the visible scene %d times", device.clears)
	}
	if app.assetBank != 1 {
		t.Fatalf("prepared bank = %d, want inactive bank 1", app.assetBank)
	}
	want := []string{colorCoverPath(1), grayCoverPath(1)}
	if len(device.uploads) != len(want) {
		t.Fatalf("uploads = %v, want %v", device.uploads, want)
	}
	for index := range want {
		if device.uploads[index] != want[index] {
			t.Fatalf("uploads = %v, want %v", device.uploads, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	t.Parallel()
	if got := cleanText("Beyoncé — déjà vu… 🎵"); got != "Beyonce - deja vu... ?" {
		t.Fatalf("cleanText = %q", got)
	}
}

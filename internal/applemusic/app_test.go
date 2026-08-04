package applemusic

import (
	"context"
	"errors"
	"testing"

	barapi "github.com/matteing/busybar-apple-music/internal/busybar"
)

type fakeBar struct {
	drawings  []barapi.Drawing
	clears    int
	drawCalls int
	failDraws int
}

func (f *fakeBar) UploadAsset(context.Context, string, string, []byte) error { return nil }
func (f *fakeBar) DeleteAssets(context.Context, string) error                { return nil }
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

func TestViewOptionDefaultsToTitles(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.view != sceneText {
		t.Fatalf("default view = %s, want %s", opts.view, sceneText)
	}
}

func TestViewOptionSelectsVisualizer(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions([]string{"--view", viewVisualizer})
	if err != nil {
		t.Fatal(err)
	}
	if opts.view != sceneVisualizer {
		t.Fatalf("selected view = %s, want %s", opts.view, sceneVisualizer)
	}
}

func TestViewOptionRejectsUnknownValue(t *testing.T) {
	t.Parallel()
	if _, err := parseOptions([]string{"--view", "both"}); err == nil {
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
	if len(elements) != 3 || elements[0].Path != grayCover {
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
	if len(elements) != 1 || elements[0].ID != "visualizer" || elements[0].Path != "visualizer-07.png" {
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
		visualFrames: [][]byte{{1}}, assetsReady: true, renderDirty: true, clearFirst: true,
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
	if app.renderDirty || device.clears != 2 || device.drawCalls != 2 {
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
	if len(elements) != 3 || elements[0].Path != grayCover || elements[1].Text != current.Name {
		t.Fatalf("paused composition = %#v", elements)
	}
}

func TestCleanText(t *testing.T) {
	t.Parallel()
	if got := cleanText("Beyoncé — déjà vu… 🎵"); got != "Beyonce - deja vu... ?" {
		t.Fatalf("cleanText = %q", got)
	}
}

package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	barapi "github.com/matteing/busybar-apple-music/internal/busybar"
	"github.com/matteing/busybar-apple-music/internal/media"
)

const (
	ApplicationID      = "busybar_apple_music"
	defaultSource      = "https://matteing.com/api/now-playing"
	displayWidth       = 72
	displayHeight      = 16
	outerPadding       = 1
	artworkSize        = displayHeight - 2*outerPadding
	artworkX           = outerPadding
	artworkY           = outerPadding
	contentX           = artworkX + artworkSize + 1
	contentY           = outerPadding
	contentWidth       = displayWidth - contentX - outerPadding
	contentHeight      = displayHeight - 2*outerPadding
	textWidth          = contentWidth
	titleY             = contentY
	artistY            = contentY + 7
	viewTitles         = "titles"
	viewVisualizer     = "visualizer"
	defaultScrollRate  = 1500
	scrollTime         = 6 * time.Second
	scrollRest         = 3 * time.Second
	pollInterval       = 10 * time.Second
	clearSettle        = 750 * time.Millisecond
	assetRetryDelay    = 300 * time.Millisecond
	assetDeleteRetries = 4
	// A higher-priority native BUSY Bar view can legitimately reject draws for
	// minutes. Retry at the metadata cadence instead of hammering the firmware.
	redrawRetryDelay = 10 * time.Second
	noRepeatDelay    = 60 * time.Second
	colorCover       = "cover.png"
	grayCover        = "cover-gray.png"
)

type options struct {
	host        string
	token       string
	source      string
	priority    int
	keepDisplay bool
	view        scene
}

type Track struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	ArtistName  string `json:"artistName"`
	CoverURL    string `json:"coverUrl"`
	HQCoverURL  string `json:"hqCoverUrl"`
	ArtworkURL  string `json:"artworkUrl"`
	AlbumArtURL string `json:"albumArtUrl"`
	Album       *Album `json:"album"`
}

type Album struct {
	CoverURL   string `json:"coverUrl"`
	HQCoverURL string `json:"hqCoverUrl"`
}

type snapshot struct {
	Track        *Track  `json:"track"`
	IsPlaying    bool    `json:"isPlaying"`
	RecentAlbums []Track `json:"recentAlbums"`
}

type sourceClient struct {
	url  string
	http *http.Client
}

type bar interface {
	UploadAsset(context.Context, string, string, []byte) error
	DeleteAssets(context.Context, string) error
	Draw(context.Context, barapi.Drawing) error
	Clear(context.Context, string) error
}

type scene uint8

const (
	sceneText scene = iota
	sceneVisualizer
)

type textPhase uint8

const (
	phaseTitle textPhase = iota
	phaseAfterTitle
	phaseArtist
	phaseAfterArtist
)

type application struct {
	options options
	bar     bar
	source  sourceClient

	track        *Track
	trackKey     string
	playing      bool
	scene        scene
	phase        textPhase
	visualFrame  int
	visualFrames [][]byte
	assetsReady  bool
	renderDirty  bool
	clearFirst   bool
}

type pollResult struct {
	value snapshot
	err   error
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	device := barapi.New(opts.host, opts.token)
	version, err := device.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", opts.host, err)
	}
	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", opts.host, version.APISemver)

	app := &application{
		options: opts,
		bar:     device,
		scene:   opts.view,
		source: sourceClient{
			url:  opts.source,
			http: &http.Client{Timeout: 10 * time.Second},
		},
	}
	if !opts.keepDisplay {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = device.Clear(cleanup, ApplicationID)
		}()
	}
	return app.run(ctx)
}

func parseOptions(args []string) (options, error) {
	var result options
	view := viewTitles
	flags := flag.NewFlagSet("applemusic", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&result.host, "host", envOr("BUSYBAR_HOST", "10.0.4.20"), "BUSY Bar hostname or IP")
	flags.StringVar(&result.token, "token", envOr("BUSYBAR_TOKEN", ""), "Wi-Fi API token")
	flags.StringVar(&result.source, "source", defaultSource, "now-playing JSON endpoint")
	flags.IntVar(&result.priority, "priority", 100, "display priority")
	flags.BoolVar(&result.keepDisplay, "keep-display", false, "leave the final frame on exit")
	flags.StringVar(&view, "view", viewTitles, "view to display: titles or visualizer")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: applemusic [flags]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return result, err
	}
	if flags.NArg() != 0 {
		return result, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	switch view {
	case viewTitles:
		result.view = sceneText
	case viewVisualizer:
		result.view = sceneVisualizer
	default:
		return result, fmt.Errorf("invalid --view %q: must be %q or %q", view, viewTitles, viewVisualizer)
	}
	return result, nil
}

func (a *application) run(ctx context.Context) error {
	results := make(chan pollResult, 1)
	polling := false
	startPoll := func() {
		if polling {
			return
		}
		polling = true
		go func() {
			value, err := a.source.fetch(ctx)
			select {
			case results <- pollResult{value: value, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	polls := time.NewTicker(pollInterval)
	defer polls.Stop()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	a.schedule(timer)
	startPoll()

	fmt.Printf("Apple Music is active in %s view; polling every %s.\n", a.scene, pollInterval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-polls.C:
			startPoll()
		case result := <-results:
			polling = false
			if result.err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", result.err)
			} else if err := a.applySnapshot(ctx, result.value); err != nil {
				fmt.Fprintf(os.Stderr, "warning: refresh display: %v\n", err)
			}
			a.schedule(timer)
		case <-timer.C:
			if err := a.animationStep(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: render: %v\n", err)
			}
			a.schedule(timer)
		}
	}
}

func (a *application) schedule(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	delay := time.Hour
	if a.renderDirty {
		delay = redrawRetryDelay
	} else if a.playing && a.track != nil {
		if a.scene == sceneVisualizer {
			delay = time.Second / media.VisualizerFPS
		} else if a.phase == phaseTitle || a.phase == phaseArtist {
			delay = scrollTime
		} else {
			delay = scrollRest
		}
	}
	timer.Reset(delay)
}

func (a *application) applySnapshot(ctx context.Context, value snapshot) error {
	candidate := value.Track
	// Once paused, preserve the exact last track even if a provider briefly
	// exposes a different queued item. The paused framebuffer is a freeze, not
	// another now-playing transition.
	if !value.IsPlaying && a.track != nil {
		candidate = nil
	}
	if candidate == nil && a.track == nil && len(value.RecentAlbums) != 0 {
		candidate = &value.RecentAlbums[0]
	}
	if candidate != nil {
		candidate.normalize()
		if candidate.Name == "" && candidate.Artist == "" {
			candidate = nil
		}
	}
	trackChanged := candidate != nil && fingerprint(candidate) != a.trackKey
	if trackChanged {
		a.track = candidate
		a.trackKey = fingerprint(candidate)
		a.phase = phaseTitle
		a.visualFrame = 0
		a.assetsReady = false
		a.markDirty(false)
		if value.IsPlaying {
			fmt.Printf("Now playing: %s — %s\n", candidate.Name, candidate.Artist)
		} else {
			fmt.Printf("Loaded last track: %s — %s\n", candidate.Name, candidate.Artist)
		}
	}
	if a.track == nil {
		a.playing = false
		return nil
	}
	stateChanged := a.playing != value.IsPlaying
	a.playing = value.IsPlaying
	if trackChanged || stateChanged {
		if stateChanged && !trackChanged {
			a.markDirty(true)
		}
		return a.reconcile(ctx)
	}
	return nil
}

func (a *application) markDirty(clearFirst bool) {
	a.renderDirty = true
	a.clearFirst = a.clearFirst || clearFirst
}

// reconcile is the only path that commits a complete scene. Partial animation
// updates are suppressed while it is dirty, so a failed clear/draw can never
// be followed by frames from another scene.
func (a *application) reconcile(ctx context.Context) error {
	if !a.renderDirty {
		return nil
	}
	if a.track == nil {
		a.renderDirty = false
		a.clearFirst = false
		return nil
	}
	if !a.assetsReady {
		if err := a.prepareTrack(ctx, a.track); err != nil {
			return err
		}
		a.assetsReady = true
		// prepareTrack clears the application before replacing its assets.
		a.clearFirst = false
	}
	if a.clearFirst {
		if err := a.clear(ctx); err != nil {
			return err
		}
	}
	if err := a.drawFullScene(ctx, !a.playing); err != nil {
		// The device may have applied only part of a failed request. Clear on
		// the next retry before drawing another complete composition.
		a.clearFirst = true
		return err
	}
	a.renderDirty = false
	a.clearFirst = false
	return nil
}

func (a *application) animationStep(ctx context.Context) error {
	if a.renderDirty {
		return a.reconcile(ctx)
	}
	if !a.playing || a.track == nil {
		return nil
	}
	var err error
	if a.scene == sceneVisualizer {
		if len(a.visualFrames) == 0 {
			a.assetsReady = false
			a.markDirty(true)
			return fmt.Errorf("visualizer has no prepared frames")
		}
		a.visualFrame = (a.visualFrame + 1) % len(a.visualFrames)
		err = a.drawVisualizerFrame(ctx)
	} else {
		a.phase = (a.phase + 1) % 4
		err = a.drawText(ctx, false, false)
	}
	if err != nil {
		a.markDirty(true)
	}
	return err
}

func (a *application) prepareTrack(ctx context.Context, track *Track) error {
	if err := a.clear(ctx); err != nil {
		return err
	}
	if err := a.deleteAssets(ctx); err != nil {
		return err
	}
	artwork, err := media.PrepareArtwork(track.cover(), artworkSize)
	if err != nil {
		return err
	}
	var frames [][]byte
	if a.scene == sceneVisualizer {
		frames, err = media.GenerateVisualizerPNGs(contentWidth, contentHeight, artwork.Palette)
		if err != nil {
			return err
		}
	}
	assets := []struct {
		name string
		data []byte
	}{
		{name: colorCover, data: artwork.ColorPNG},
		{name: grayCover, data: artwork.GrayscalePNG},
	}
	for _, asset := range assets {
		if err := a.bar.UploadAsset(ctx, ApplicationID, asset.name, asset.data); err != nil {
			return fmt.Errorf("upload %s: %w", asset.name, err)
		}
	}
	for index, frame := range frames {
		name := fmt.Sprintf("visualizer-%02d.png", index)
		if err := a.bar.UploadAsset(ctx, ApplicationID, name, frame); err != nil {
			return fmt.Errorf("upload visualizer frame %d: %w", index+1, err)
		}
	}
	a.visualFrames = frames
	return nil
}

func (a *application) drawFullScene(ctx context.Context, paused bool) error {
	if a.track == nil {
		return nil
	}
	if a.scene == sceneVisualizer {
		return a.drawVisualizer(ctx, paused, true)
	}
	return a.drawText(ctx, paused, true)
}

func (a *application) drawText(ctx context.Context, paused, includeCover bool) error {
	titleRate, artistRate := 0, 0
	if !paused && a.phase == phaseTitle && shouldScroll(a.track.Name) {
		titleRate = defaultScrollRate
	}
	if !paused && a.phase == phaseArtist && shouldScroll(a.track.Artist) {
		artistRate = defaultScrollRate
	}
	titleColor, artistColor := "#FFFFFFFF", "#A8A8A8FF"
	if paused {
		titleColor, artistColor = "#777777FF", "#505050FF"
	}
	elements := make([]barapi.Element, 0, 3)
	if includeCover {
		elements = append(elements, imageElement("cover", coverPath(paused), artworkX, artworkY))
	}
	elements = append(elements,
		textElement("title", a.track.Name, titleColor, titleY, titleRate),
		textElement("artist", a.track.Artist, artistColor, artistY, artistRate),
	)
	return a.draw(ctx, elements)
}

func (a *application) drawVisualizer(ctx context.Context, paused, includeCover bool) error {
	path := fmt.Sprintf("visualizer-%02d.png", a.visualFrame)
	if paused {
		gray, err := media.GrayscalePNG(a.visualFrames[a.visualFrame])
		if err != nil {
			return err
		}
		path = "visualizer-paused.png"
		if err := a.bar.UploadAsset(ctx, ApplicationID, path, gray); err != nil {
			return fmt.Errorf("upload paused visualizer: %w", err)
		}
	}
	elements := make([]barapi.Element, 0, 2)
	if includeCover {
		elements = append(elements, imageElement("cover", coverPath(paused), artworkX, artworkY))
	}
	elements = append(elements, imageElement("visualizer", path, contentX, contentY))
	return a.draw(ctx, elements)
}

func (a *application) drawVisualizerFrame(ctx context.Context) error {
	return a.draw(ctx, []barapi.Element{
		imageElement("visualizer", fmt.Sprintf("visualizer-%02d.png", a.visualFrame), contentX, contentY),
	})
}

func (a *application) draw(ctx context.Context, elements []barapi.Element) error {
	return a.bar.Draw(ctx, barapi.Drawing{
		ApplicationName: ApplicationID,
		Priority:        a.options.priority,
		Elements:        elements,
	})
}

func (a *application) clear(ctx context.Context) error {
	if err := a.bar.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("clear Apple Music scene: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(clearSettle):
		return nil
	}
}

func (a *application) deleteAssets(ctx context.Context) error {
	var last error
	for attempt := 1; attempt <= assetDeleteRetries; attempt++ {
		last = a.bar.DeleteAssets(ctx, ApplicationID)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(assetRetryDelay):
		}
	}
	return fmt.Errorf("delete Apple Music assets after %d attempts: %w", assetDeleteRetries, last)
}

func (s sourceClient) fetch(ctx context.Context) (snapshot, error) {
	var result snapshot
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return result, err
	}
	response, err := s.http.Do(request)
	if err != nil {
		return result, fmt.Errorf("fetch now playing: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("fetch now playing: server returned %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode now playing: %w", err)
	}
	return result, nil
}

func (t *Track) normalize() {
	if t.Name == "" {
		t.Name = t.Title
	}
	if t.Artist == "" {
		t.Artist = t.ArtistName
	}
	t.Name = cleanText(t.Name)
	t.Artist = cleanText(t.Artist)
}

func (t *Track) cover() string {
	candidates := []string{t.HQCoverURL, t.CoverURL, t.ArtworkURL, t.AlbumArtURL}
	if t.Album != nil {
		candidates = append(candidates, t.Album.HQCoverURL, t.Album.CoverURL)
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func fingerprint(track *Track) string {
	return track.ID + "\x00" + track.Name + "\x00" + track.Artist + "\x00" + track.cover()
}

func shouldScroll(value string) bool {
	return len([]rune(value))*6 > textWidth
}

func imageElement(id, path string, x, y int) barapi.Element {
	opacity := 100
	return barapi.Element{ID: id, Type: "image", X: x, Y: y, Path: path, Display: "front", Opacity: &opacity}
}

func textElement(id, text, color string, y, rate int) barapi.Element {
	scroll := rate
	return barapi.Element{
		ID: id, Type: "text", X: contentX, Y: y, Text: text,
		Font: "small", Color: color, Display: "front", Width: textWidth,
		ScrollRate: &scroll, ScrollRepeatDelay: int(noRepeatDelay / time.Millisecond),
	}
}

func coverPath(paused bool) string {
	if paused {
		return grayCover
	}
	return colorCover
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (s scene) String() string {
	if s == sceneVisualizer {
		return viewVisualizer
	}
	return viewTitles
}

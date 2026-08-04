package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	barapi "github.com/matteing/busybar-apps/internal/busybar"
	"github.com/matteing/busybar-apps/internal/media"
)

const (
	ApplicationID      = "busybar_apple_music"
	coverFilename      = "cover.png"
	defaultSource      = "https://matteing.com/api/now-playing"
	displayWidth       = 72
	viewPadding        = 2
	coverSize          = 14
	contentGap         = 2
	clearSettle        = 750 * time.Millisecond
	viewSwitchDebounce = 750 * time.Millisecond
	assetRetryDelay    = 300 * time.Millisecond
	assetDeleteRetries = 4
	noRepeatDelay      = 60 * time.Second
	recentSize         = 14
	recentGap          = 2
	maxRecent          = 8
	recentSubframes    = 12
	animationFrameTime = time.Second / 60
	waveformFrameTime  = time.Second / media.WaveformFramesPerSecond
)

type options struct {
	host        string
	token       string
	source      string
	poll        time.Duration
	scrollRate  int
	scrollTime  time.Duration
	scrollRest  time.Duration
	recentSpeed time.Duration
	priority    int
	demo        bool
	once        bool
	keepDisplay bool
}

type Track struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	ArtistName    string `json:"artistName"`
	CoverURL      string `json:"coverUrl"`
	HQCoverURL    string `json:"hqCoverUrl"`
	ArtworkURL    string `json:"artworkUrl"`
	AlbumArtURL   string `json:"albumArtUrl"`
	DominantColor string `json:"dominantColor"`
	URL           string `json:"url"`
	Album         *Album `json:"album"`
}

type Album struct {
	CoverURL   string `json:"coverUrl"`
	HQCoverURL string `json:"hqCoverUrl"`
}

type nowPlayingResponse struct {
	Track        *Track  `json:"track"`
	IsPlaying    bool    `json:"isPlaying"`
	RecentAlbums []Track `json:"recentAlbums"`
	StartedAt    any     `json:"startedAt"`
}

type sourceClient struct {
	url  string
	http *http.Client
}

type busyBar interface {
	UploadAsset(context.Context, string, string, []byte) error
	DeleteAssets(context.Context, string) error
	Draw(context.Context, barapi.DisplayElements) error
	Clear(context.Context, string) error
}

type inputBar interface {
	StreamInputs(context.Context, func(barapi.InputEvent)) error
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	bar := barapi.New(opts.host, opts.token)
	version, err := bar.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", opts.host, err)
	}
	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", opts.host, version.APISemver)
	// Starting from a clean application layer ensures old element animation state
	// cannot retain a previous font or scroll rate across binary restarts.
	if err := bar.Clear(ctx, ApplicationID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not clear previous Apple Music display: %v\n", err)
	} else {
		// Display commands are applied asynchronously by the firmware. Give the
		// clear a moment to land so stale element IDs cannot overlap the redraw.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(clearSettle):
		}
	}
	if !opts.keepDisplay {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := bar.Clear(cleanupCtx, ApplicationID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not clear display: %v\n", err)
			}
		}()
	}

	app := &application{
		options: opts,
		bar:     bar,
		source: sourceClient{
			url:  opts.source,
			http: &http.Client{Timeout: 15 * time.Second},
		},
	}
	return app.run(ctx)
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("apple-music", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&opts.host, "host", envOr("BUSYBAR_HOST", "10.0.4.20"), "BUSY Bar hostname or IP")
	flags.StringVar(&opts.token, "token", envOr("BUSYBAR_TOKEN", ""), "Wi-Fi API token (or BUSYBAR_TOKEN)")
	flags.StringVar(&opts.source, "source", defaultSource, "now-playing JSON endpoint")
	flags.DurationVar(&opts.poll, "poll", 10*time.Second, "now-playing polling interval")
	flags.IntVar(&opts.scrollRate, "scroll-rate", 1500, "text scroll speed in pixels per minute")
	flags.DurationVar(&opts.scrollTime, "scroll-time", 6*time.Second, "time allotted to each scrolling row")
	flags.DurationVar(&opts.scrollRest, "scroll-rest", 3*time.Second, "still time between scrolling rows")
	flags.DurationVar(&opts.recentSpeed, "recent-speed", 40*time.Millisecond, "time per pixel of recent-cover travel (lower is faster)")
	flags.IntVar(&opts.priority, "priority", 100, "BUSY Bar drawing priority")
	flags.BoolVar(&opts.demo, "demo", false, "show the most recent album when nothing is playing")
	flags.BoolVar(&opts.once, "once", false, "fetch and draw once, then exit")
	flags.BoolVar(&opts.keepDisplay, "keep-display", false, "do not clear the display on exit")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Show Apple Music now-playing information on a BUSY Bar.")
		fmt.Fprintln(flags.Output(), "\nUsage: busyctl apple-music [flags]")
		fmt.Fprintln(flags.Output())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if opts.poll <= 0 || opts.scrollTime <= 0 || opts.scrollRest <= 0 || opts.recentSpeed <= 0 || opts.scrollRate < 0 {
		return opts, fmt.Errorf("duration flags must be positive; scroll-rate cannot be negative")
	}
	return opts, nil
}

type application struct {
	options          options
	bar              busyBar
	source           sourceClient
	track            *Track
	isPlaying        bool
	coverURL         string
	recent           []Track
	recentArt        string
	recentX          float64
	recentStripWidth int
	recentTileWidths []int
	waveformKey      string
	waveformFrame    int
	animationAt      time.Time
	lastDrawing      string
	lastMode         string
	phase            scrollPhase
	view             playingView
	assetsNeedReset  bool
	lastViewInput    time.Time
}

type scrollPhase uint8

const (
	phaseTitle scrollPhase = iota
	phaseRestAfterTitle
	phaseArtist
	phaseRestAfterArtist
)

type playingView uint8

const (
	viewText playingView = iota
	viewWaveform
)

func (a *application) run(ctx context.Context) error {
	if _, err := a.refresh(ctx); err != nil {
		return err
	}
	if err := a.render(ctx); err != nil {
		return err
	}
	if a.options.once {
		return nil
	}

	fmt.Printf("Apple Music is active; polling %s every %s. Press Ctrl+C to stop.\n", a.options.source, a.options.poll)
	pollTicker := time.NewTicker(a.options.poll)
	defer pollTicker.Stop()
	phaseTimer := time.NewTimer(a.phaseDuration())
	defer phaseTimer.Stop()
	a.animationAt = time.Now()
	inputs := make(chan barapi.InputEvent)
	if source, ok := a.bar.(inputBar); ok {
		go a.streamInputs(ctx, source, inputs)
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Stopping Apple Music…")
			return nil
		case <-pollTicker.C:
			changed, err := a.refresh(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				continue
			}
			if changed {
				a.phase = phaseTitle
				a.animationAt = time.Now()
				resetTimer(phaseTimer, a.phaseDuration())
			}
			if err := a.render(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: display update failed: %v\n", err)
			}
		case input := <-inputs:
			if a.track == nil || input.EncoderDelta == 0 {
				continue
			}
			if time.Since(a.lastViewInput) < viewSwitchDebounce {
				continue
			}
			a.lastViewInput = time.Now()
			a.rotateView(input.EncoderDelta)
			a.animationAt = time.Now()
			resetTimer(phaseTimer, a.phaseDuration())
			if err := a.render(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: dial display update failed: %v\n", err)
			}
		case <-phaseTimer.C:
			frameStarted := time.Now()
			if a.track == nil {
				elapsed := frameStarted.Sub(a.animationAt)
				if elapsed <= 0 {
					elapsed = animationFrameTime
				}
				a.advanceRecentBy(elapsed)
			} else {
				a.advancePlaying()
			}
			a.animationAt = frameStarted
			if err := a.render(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: carousel update failed: %v\n", err)
			}
			delay := a.phaseDuration()
			if a.track == nil || a.view == viewWaveform {
				delay -= time.Since(frameStarted)
				if delay < time.Millisecond {
					delay = time.Millisecond
				}
			}
			phaseTimer.Reset(delay)
		}
	}
}

func (a *application) refresh(ctx context.Context) (bool, error) {
	response, err := a.source.fetch(ctx)
	if err != nil {
		return false, fmt.Errorf("fetch now playing: %w", err)
	}

	track := response.Track
	if !response.IsPlaying {
		track = nil
	}
	if track == nil && a.options.demo && len(response.RecentAlbums) > 0 {
		track = &response.RecentAlbums[0]
	}
	if track != nil {
		track.normalize()
		if track.Name == "" && track.Artist == "" {
			track = nil
		}
	}

	recent := normalizeRecent(response.RecentAlbums)
	trackChanged := trackFingerprint(a.track) != trackFingerprint(track)
	recentChanged := recentFingerprint(a.recent) != recentFingerprint(recent)
	changed := trackChanged || (track == nil && recentChanged)
	a.track = track
	a.recent = recent
	a.isPlaying = response.IsPlaying
	if changed {
		a.assetsNeedReset = true
		a.lastDrawing = ""
		a.recentX = 0
		if trackChanged && track != nil {
			fmt.Printf("Now playing: %s — %s\n", track.Name, track.Artist)
		} else if trackChanged {
			fmt.Println("Apple Music is not currently playing.")
		}
	}
	return changed, nil
}

func (a *application) render(ctx context.Context) error {
	if a.assetsNeedReset {
		if err := a.resetAssets(ctx); err != nil {
			return err
		}
	}
	mode := "playing_text"
	if a.track == nil {
		mode = "recent"
	} else if a.view == viewWaveform {
		mode = "playing_waveform"
	}
	if err := a.prepareMode(ctx, mode); err != nil {
		return err
	}
	if a.track == nil {
		return a.renderRecent(ctx)
	}

	coverURL := a.track.cover()
	if coverURL != "" && coverURL != a.coverURL {
		artwork, err := media.DownloadSquarePNG(coverURL, coverSize)
		if err != nil {
			return err
		}
		if err := a.bar.UploadAsset(ctx, ApplicationID, coverFilename, artwork); err != nil {
			return fmt.Errorf("upload album cover: %w", err)
		}
		a.coverURL = coverURL
	}
	if a.view == viewWaveform {
		waveformKey := trackFingerprint(a.track) + "\x00" + a.track.DominantColor
		if waveformKey != a.waveformKey {
			frames, err := media.WaveformPNGs(displayWidth-(viewPadding+coverSize+contentGap)-viewPadding, coverSize, a.track.DominantColor)
			if err != nil {
				return fmt.Errorf("generate waveform: %w", err)
			}
			for index, frame := range frames {
				filename := fmt.Sprintf("waveform-%02d.png", index)
				if err := a.bar.UploadAsset(ctx, ApplicationID, filename, frame); err != nil {
					return fmt.Errorf("upload waveform frame %d: %w", index+1, err)
				}
			}
			a.waveformKey = waveformKey
		}
	}

	fingerprint := fmt.Sprintf("%s\x00view:%d\x00phase:%d\x00frame:%d", trackFingerprint(a.track), a.view, a.phase, a.waveformFrame)
	if a.lastDrawing == fingerprint {
		return nil
	}

	textX := viewPadding
	elements := make([]barapi.Element, 0, 3)
	if coverURL != "" {
		elements = append(elements, barapi.Element{
			ID: "cover", Type: "image", X: viewPadding, Y: 1, Path: coverFilename,
			Display: "front", Opacity: intPointer(100),
		})
		textX = viewPadding + coverSize + contentGap
	}
	textWidth := displayWidth - textX - viewPadding
	if a.view == viewWaveform {
		elements = append(elements, barapi.Element{
			ID: "waveform", Type: "image", X: textX, Y: 1,
			Path:    fmt.Sprintf("waveform-%02d.png", a.waveformFrame),
			Display: "front", Opacity: intPointer(100),
		})
	} else {
		titleRate, artistRate := 0, 0
		if a.phase == phaseTitle && shouldScroll(a.track.Name, textWidth) {
			titleRate = a.options.scrollRate
		}
		if a.phase == phaseArtist && shouldScroll(a.track.Artist, textWidth) {
			artistRate = a.options.scrollRate
		}
		textElement := func(id, text, colorValue string, y, scrollRate int) barapi.Element {
			rate := scrollRate
			return barapi.Element{
				ID: id, Type: "text", X: textX, Y: y, Text: text,
				Font: "small", Color: colorValue, Display: "front", Width: textWidth,
				ScrollRate: &rate, ScrollRepeatDelay: int(noRepeatDelay / time.Millisecond),
			}
		}
		elements = append(elements,
			textElement("title", a.track.Name, "#FFFFFFFF", 0, titleRate),
			textElement("artist", a.track.Artist, "#A8A8A8FF", 7, artistRate),
		)
	}

	if err := a.bar.Draw(ctx, barapi.DisplayElements{
		ApplicationName: ApplicationID,
		Priority:        a.options.priority,
		Elements:        elements,
	}); err != nil {
		return err
	}
	a.lastDrawing = fingerprint
	a.lastMode = mode
	return nil
}

func (a *application) prepareMode(ctx context.Context, mode string) error {
	if a.lastMode == "" || a.lastMode == mode {
		return nil
	}
	if err := a.bar.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("switch display mode: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(clearSettle):
	}
	a.lastDrawing = ""
	return nil
}

func (a *application) resetAssets(ctx context.Context) error {
	if err := a.bar.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("clear display before refreshing assets: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(clearSettle):
	}
	var deleteErr error
	for attempt := 0; attempt < assetDeleteRetries; attempt++ {
		deleteErr = a.bar.DeleteAssets(ctx, ApplicationID)
		if deleteErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(assetRetryDelay):
		}
	}
	if deleteErr != nil {
		return fmt.Errorf("delete stale Apple Music assets after %d attempts: %w", assetDeleteRetries, deleteErr)
	}
	a.coverURL = ""
	a.waveformKey = ""
	a.recentArt = ""
	a.recentStripWidth = 0
	a.recentTileWidths = nil
	a.lastDrawing = ""
	a.lastMode = ""
	a.assetsNeedReset = false
	return nil
}

func (a *application) renderRecent(ctx context.Context) error {
	artFingerprint := recentFingerprint(a.recent)
	if a.recentArt != "" && a.recentArt != artFingerprint {
		if err := a.bar.Clear(ctx, ApplicationID); err != nil {
			return fmt.Errorf("refresh recent albums: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(clearSettle):
		}
		a.lastDrawing = ""
	}
	if a.recentArt != artFingerprint {
		artwork := make([][]byte, 0, len(a.recent))
		for index, album := range a.recent {
			cover, err := media.DownloadSquarePNG(album.cover(), recentSize)
			if err != nil {
				return fmt.Errorf("download recent album %d: %w", index+1, err)
			}
			artwork = append(artwork, cover)
		}
		if len(artwork) > 0 {
			frames, width, err := media.CarouselPNGs(artwork, recentGap, displayWidth+recentSize+recentGap, recentSubframes)
			if err != nil {
				return fmt.Errorf("compose recent albums: %w", err)
			}
			tiles, tileWidths, err := media.TilePNGs(frames, displayWidth)
			if err != nil {
				return fmt.Errorf("tile recent albums: %w", err)
			}
			for phaseIndex, phaseTiles := range tiles {
				for tileIndex, tile := range phaseTiles {
					filename := fmt.Sprintf("recent-strip-%02d-%02d.png", phaseIndex, tileIndex)
					if err := a.bar.UploadAsset(ctx, ApplicationID, filename, tile); err != nil {
						return fmt.Errorf("upload recent carousel phase %d tile %d: %w", phaseIndex+1, tileIndex+1, err)
					}
				}
			}
			a.recentStripWidth = width
			a.recentTileWidths = tileWidths
		}
		a.recentArt = artFingerprint
	}

	fingerprint := fmt.Sprintf("recent:%s:x:%.4f", artFingerprint, a.recentX)
	if a.lastDrawing == fingerprint {
		return nil
	}

	var elements []barapi.Element
	if len(a.recent) == 0 {
		elements = []barapi.Element{
			{
				ID: "status", Type: "text", X: viewPadding, Y: 1,
				Text: "APPLE MUSIC", Font: "small", Color: "#FA2D48FF", Display: "front",
				Width: displayWidth - 2*viewPadding,
			},
			{
				ID: "state", Type: "text", X: viewPadding, Y: 8,
				Text: "NOT PLAYING", Font: "small", Color: "#A8A8A8FF", Display: "front",
				Width: displayWidth - 2*viewPadding,
			},
		}
	} else {
		baseOffset := int(math.Floor(a.recentX))
		fraction := a.recentX - float64(baseOffset)
		phase := int(math.Round(fraction * recentSubframes))
		if phase == recentSubframes {
			phase = 0
			baseOffset++
		}
		for cycle := -1; cycle <= 1; cycle++ {
			tileStart := 0
			for tileIndex, tileWidth := range a.recentTileWidths {
				x := viewPadding + cycle*a.recentStripWidth + tileStart - baseOffset
				if x < displayWidth && x+tileWidth > 0 {
					elements = append(elements, barapi.Element{
						ID: fmt.Sprintf("recent_%d_%d", cycle, tileIndex), Type: "image",
						X: x, Y: 1, Path: fmt.Sprintf("recent-strip-%02d-%02d.png", phase, tileIndex),
						Display: "front", Opacity: intPointer(100),
					})
				}
				tileStart += tileWidth
			}
		}
	}

	if err := a.bar.Draw(ctx, barapi.DisplayElements{
		ApplicationName: ApplicationID,
		Priority:        a.options.priority,
		Elements:        elements,
	}); err != nil {
		return err
	}
	a.lastDrawing = fingerprint
	a.lastMode = "recent"
	return nil
}

func (a *application) phaseDuration() time.Duration {
	if a.track == nil {
		return animationFrameTime
	}
	if a.phase == phaseTitle || a.phase == phaseArtist {
		return a.options.scrollTime
	}
	if a.view == viewWaveform {
		return waveformFrameTime
	}
	return a.options.scrollRest
}

func (a *application) advancePlaying() {
	if a.view == viewWaveform {
		a.waveformFrame = (a.waveformFrame + 1) % media.WaveformFrameCount
		return
	}
	switch a.phase {
	case phaseTitle:
		a.phase = phaseRestAfterTitle
	case phaseRestAfterTitle:
		a.phase = phaseArtist
	case phaseArtist:
		a.phase = phaseRestAfterArtist
	case phaseRestAfterArtist:
		a.phase = phaseTitle
	}
}

func (a *application) rotateView(delta int) {
	if delta == 0 {
		return
	}
	if a.view == viewText {
		a.view = viewWaveform
		a.waveformFrame = 0
	} else {
		a.view = viewText
		a.phase = phaseTitle
	}
	a.lastDrawing = ""
}

func (a *application) streamInputs(ctx context.Context, source inputBar, output chan<- barapi.InputEvent) {
	for ctx.Err() == nil {
		err := source.StreamInputs(ctx, func(input barapi.InputEvent) {
			select {
			case output <- input:
			case <-ctx.Done():
			}
		})
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "warning: BUSY Bar input stream reconnecting: %v\n", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func shouldScroll(text string, width int) bool {
	// The bundled small font advances at most six pixels per ASCII glyph.
	// Avoid enabling firmware scrolling for labels that already fit; doing so
	// can leave short titles clipped after scene changes.
	return utf8.RuneCountInString(text)*6 > width
}

func (a *application) advanceRecent() {
	a.advanceRecentBy(animationFrameTime)
}

func (a *application) advanceRecentBy(elapsed time.Duration) {
	if len(a.recent) == 0 || a.recentStripWidth <= 0 || a.options.recentSpeed <= 0 {
		return
	}
	stripWidth := float64(a.recentStripWidth)
	step := float64(elapsed) / float64(a.options.recentSpeed)
	a.recentX = math.Mod(a.recentX+step, stripWidth)
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func intPointer(value int) *int {
	return &value
}

func (s sourceClient) fetch(ctx context.Context) (nowPlayingResponse, error) {
	var result nowPlayingResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "busyctl-apple-music/0.2")
	resp, err := s.http.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("server returned %s", resp.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&result); err != nil {
		return result, err
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
	candidates := []string{t.CoverURL, t.HQCoverURL, t.ArtworkURL, t.AlbumArtURL}
	if t.Album != nil {
		candidates = append(candidates, t.Album.CoverURL, t.Album.HQCoverURL)
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func trackFingerprint(track *Track) string {
	if track == nil {
		return ""
	}
	return track.ID + "\x00" + track.Name + "\x00" + track.Artist + "\x00" + track.cover()
}

func normalizeRecent(albums []Track) []Track {
	recent := make([]Track, 0, min(len(albums), maxRecent))
	for _, album := range albums {
		album.normalize()
		if album.cover() == "" {
			continue
		}
		recent = append(recent, album)
		if len(recent) == maxRecent {
			break
		}
	}
	return recent
}

func recentFingerprint(albums []Track) string {
	var fingerprint strings.Builder
	for _, album := range albums {
		fingerprint.WriteString(trackFingerprint(&album))
		fingerprint.WriteByte('\n')
	}
	return fingerprint.String()
}

func cleanText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "?")
	}
	return asciiText(value)
}

// BUSY Bar's bundled bitmap fonts contain printable ASCII only. Transliterate
// common music-metadata characters and collapse each unsupported Unicode run to
// one question mark so the firmware never renders a row of missing-glyph boxes.
func asciiText(value string) string {
	var output strings.Builder
	unsupported := false
	for _, char := range value {
		if char >= 0x20 && char <= 0x7e {
			output.WriteRune(char)
			unsupported = false
			continue
		}

		replacement := asciiReplacement(char)
		if replacement != "" {
			output.WriteString(replacement)
			unsupported = false
			continue
		}
		if !unsupported {
			output.WriteByte('?')
			unsupported = true
		}
	}
	return strings.Join(strings.Fields(output.String()), " ")
}

func asciiReplacement(char rune) string {
	switch char {
	case 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å', 'Ā', 'Ă', 'Ą':
		return "A"
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'ā', 'ă', 'ą':
		return "a"
	case 'Æ':
		return "AE"
	case 'æ':
		return "ae"
	case 'Ç', 'Ć', 'Ĉ', 'Ċ', 'Č':
		return "C"
	case 'ç', 'ć', 'ĉ', 'ċ', 'č':
		return "c"
	case 'Ð', 'Ď', 'Đ':
		return "D"
	case 'ð', 'ď', 'đ':
		return "d"
	case 'È', 'É', 'Ê', 'Ë', 'Ē', 'Ĕ', 'Ė', 'Ę', 'Ě':
		return "E"
	case 'è', 'é', 'ê', 'ë', 'ē', 'ĕ', 'ė', 'ę', 'ě':
		return "e"
	case 'Ĝ', 'Ğ', 'Ġ', 'Ģ':
		return "G"
	case 'ĝ', 'ğ', 'ġ', 'ģ':
		return "g"
	case 'Ĥ', 'Ħ':
		return "H"
	case 'ĥ', 'ħ':
		return "h"
	case 'Ì', 'Í', 'Î', 'Ï', 'Ĩ', 'Ī', 'Ĭ', 'Į', 'İ':
		return "I"
	case 'ì', 'í', 'î', 'ï', 'ĩ', 'ī', 'ĭ', 'į', 'ı':
		return "i"
	case 'Ĵ':
		return "J"
	case 'ĵ':
		return "j"
	case 'Ķ':
		return "K"
	case 'ķ':
		return "k"
	case 'Ĺ', 'Ļ', 'Ľ', 'Ŀ', 'Ł':
		return "L"
	case 'ĺ', 'ļ', 'ľ', 'ŀ', 'ł':
		return "l"
	case 'Ñ', 'Ń', 'Ņ', 'Ň':
		return "N"
	case 'ñ', 'ń', 'ņ', 'ň':
		return "n"
	case 'Ò', 'Ó', 'Ô', 'Õ', 'Ö', 'Ø', 'Ō', 'Ŏ', 'Ő':
		return "O"
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø', 'ō', 'ŏ', 'ő':
		return "o"
	case 'Œ':
		return "OE"
	case 'œ':
		return "oe"
	case 'Ŕ', 'Ŗ', 'Ř':
		return "R"
	case 'ŕ', 'ŗ', 'ř':
		return "r"
	case 'Ś', 'Ŝ', 'Ş', 'Š':
		return "S"
	case 'ś', 'ŝ', 'ş', 'š':
		return "s"
	case 'ß':
		return "ss"
	case 'Ţ', 'Ť', 'Ŧ':
		return "T"
	case 'ţ', 'ť', 'ŧ':
		return "t"
	case 'Ù', 'Ú', 'Û', 'Ü', 'Ũ', 'Ū', 'Ŭ', 'Ů', 'Ű', 'Ų':
		return "U"
	case 'ù', 'ú', 'û', 'ü', 'ũ', 'ū', 'ŭ', 'ů', 'ű', 'ų':
		return "u"
	case 'Ŵ':
		return "W"
	case 'ŵ':
		return "w"
	case 'Ý', 'Ŷ', 'Ÿ':
		return "Y"
	case 'ý', 'ÿ', 'ŷ':
		return "y"
	case 'Ź', 'Ż', 'Ž':
		return "Z"
	case 'ź', 'ż', 'ž':
		return "z"
	case 'Þ':
		return "TH"
	case 'þ':
		return "th"
	case '‘', '’', '‚', '‛', '′':
		return "'"
	case '“', '”', '„', '‟', '″':
		return "\""
	case '‐', '‑', '‒', '–', '—', '―', '−':
		return "-"
	case '…':
		return "..."
	case '•', '·':
		return "-"
	case '×':
		return "x"
	case '÷':
		return "/"
	case '©':
		return "(c)"
	case '®':
		return "(R)"
	case '™':
		return "TM"
	case '\u00a0':
		return " "
	}
	return ""
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

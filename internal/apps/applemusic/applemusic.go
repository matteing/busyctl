package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	barapi "github.com/matteing/busybar-apps/internal/busybar"
	"github.com/matteing/busybar-apps/internal/media"
)

const (
	appID         = "busybar_apple_music"
	coverFilename = "cover.png"
	defaultSource = "https://matteing.com/api/now-playing"
	displayWidth  = 72
	viewPadding   = 2
	coverSize     = 10
	contentGap    = 2
	clearSettle   = 150 * time.Millisecond
	titleDelay    = 1500
	artistDelay   = 3500
	repeatDelay   = 2500
)

type options struct {
	host        string
	token       string
	source      string
	poll        time.Duration
	scrollRate  int
	priority    int
	demo        bool
	once        bool
	keepDisplay bool
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
	URL         string `json:"url"`
	Album       *Album `json:"album"`
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
	Draw(context.Context, barapi.DisplayElements) error
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
	if err := bar.Clear(ctx, appID); err != nil {
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
			if err := bar.Clear(cleanupCtx, appID); err != nil {
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
	flags.IntVar(&opts.priority, "priority", 100, "BUSY Bar drawing priority")
	flags.BoolVar(&opts.demo, "demo", false, "show the most recent album when nothing is playing")
	flags.BoolVar(&opts.once, "once", false, "fetch and draw once, then exit")
	flags.BoolVar(&opts.keepDisplay, "keep-display", false, "do not clear the display on exit")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Show Apple Music now-playing information on a BUSY Bar.")
		fmt.Fprintln(flags.Output(), "\nUsage: busybar apple-music [flags]")
		fmt.Fprintln(flags.Output())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if opts.poll <= 0 || opts.scrollRate < 0 {
		return opts, fmt.Errorf("poll must be positive and scroll-rate cannot be negative")
	}
	return opts, nil
}

type application struct {
	options     options
	bar         busyBar
	source      sourceClient
	track       *Track
	isPlaying   bool
	coverURL    string
	lastDrawing string
}

func (a *application) run(ctx context.Context) error {
	if err := a.refresh(ctx); err != nil {
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

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Stopping Apple Music…")
			return nil
		case <-pollTicker.C:
			if err := a.refresh(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				continue
			}
			if err := a.render(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: display update failed: %v\n", err)
			}
		}
	}
}

func (a *application) refresh(ctx context.Context) error {
	response, err := a.source.fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch now playing: %w", err)
	}

	track := response.Track
	if track == nil && a.options.demo && len(response.RecentAlbums) > 0 {
		track = &response.RecentAlbums[0]
	}
	if track != nil {
		track.normalize()
		if track.Name == "" && track.Artist == "" {
			track = nil
		}
	}

	changed := trackFingerprint(a.track) != trackFingerprint(track)
	a.track = track
	a.isPlaying = response.IsPlaying
	if changed {
		a.lastDrawing = ""
		if track != nil {
			fmt.Printf("Now playing: %s — %s\n", track.Name, track.Artist)
		} else {
			fmt.Println("Apple Music is not currently playing.")
		}
	}
	return nil
}

func (a *application) render(ctx context.Context) error {
	if a.track == nil {
		fingerprint := "not-playing"
		if a.lastDrawing == fingerprint {
			return nil
		}
		drawing := barapi.DisplayElements{
			ApplicationName: appID,
			Priority:        a.options.priority,
			Elements: []barapi.Element{
				{
					ID: "status", Type: "text", X: viewPadding, Y: 1,
					Text: "APPLE MUSIC", Font: "small", Color: "#FA2D48FF", Display: "front",
					Width: displayWidth - 2*viewPadding,
				},
				{
					ID: "state", Type: "text", X: viewPadding, Y: 10,
					Text: "NOT PLAYING", Font: "small", Color: "#A8A8A8FF", Display: "front",
					Width: displayWidth - 2*viewPadding,
				},
			},
		}
		if err := a.bar.Draw(ctx, drawing); err != nil {
			return err
		}
		a.lastDrawing = fingerprint
		return nil
	}

	coverURL := a.track.cover()
	if coverURL != "" && coverURL != a.coverURL {
		artwork, err := media.DownloadSquarePNG(coverURL, coverSize)
		if err != nil {
			return err
		}
		if err := a.bar.UploadAsset(ctx, appID, coverFilename, artwork); err != nil {
			return fmt.Errorf("upload album cover: %w", err)
		}
		a.coverURL = coverURL
	}

	fingerprint := trackFingerprint(a.track)
	if a.lastDrawing == fingerprint {
		return nil
	}

	textX := viewPadding
	elements := make([]barapi.Element, 0, 3)
	if coverURL != "" {
		elements = append(elements, barapi.Element{
			ID: "cover", Type: "image", X: viewPadding, Y: 3, Path: coverFilename,
			Display: "front", Opacity: 100,
		})
		textX = viewPadding + coverSize + contentGap
	}
	textWidth := displayWidth - textX - viewPadding
	textElement := func(id, text, fontName, colorValue string, y, startDelay int) barapi.Element {
		return barapi.Element{
			ID: id, Type: "text", X: textX, Y: y, Text: text,
			Font: fontName, Color: colorValue, Display: "front", Width: textWidth,
			ScrollRate: a.options.scrollRate, ScrollStartDelay: startDelay, ScrollRepeatDelay: repeatDelay,
		}
	}
	elements = append(elements,
		textElement("title", a.track.Name, "small", "#FFFFFFFF", 0, titleDelay),
		textElement("artist", a.track.Artist, "small", "#A8A8A8FF", 7, artistDelay),
	)

	if err := a.bar.Draw(ctx, barapi.DisplayElements{
		ApplicationName: appID,
		Priority:        a.options.priority,
		Elements:        elements,
	}); err != nil {
		return err
	}
	a.lastDrawing = fingerprint
	return nil
}

func (s sourceClient) fetch(ctx context.Context) (nowPlayingResponse, error) {
	var result nowPlayingResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "busybar-apple-music/0.1")
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

package hackernews

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

const (
	ApplicationID       = "busybar_hacker_news"
	defaultAPI          = "https://hacker-news.firebaseio.com/v0"
	displayWidth        = 72
	displayHeight       = 16
	logoWidth           = 18
	headlineX           = 18
	headlineGap         = 14
	visibleStories      = 3
	storyCount          = 9
	logoFrameCount      = 12
	animationFrameTime  = 100 * time.Millisecond
	clearSettle         = 150 * time.Millisecond
	defaultPageDuration = 15 * time.Second
)

type options struct {
	host         string
	token        string
	api          string
	poll         time.Duration
	pageDuration time.Duration
	priority     int
	once         bool
	keepDisplay  bool
}

type Story struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Deleted bool   `json:"deleted"`
	Dead    bool   `json:"dead"`
}

type busyBar interface {
	UploadAsset(context.Context, string, string, []byte) error
	Draw(context.Context, barapi.DisplayElements) error
	Clear(context.Context, string) error
}

type sourceClient struct {
	baseURL string
	http    *http.Client
}

type application struct {
	options    options
	bar        busyBar
	source     sourceClient
	stories    []Story
	page       int
	frame      int
	pageStart  time.Time
	assetReady bool
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
	if err := bar.Clear(ctx, ApplicationID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not clear previous Hacker News display: %v\n", err)
	} else {
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
			baseURL: strings.TrimRight(opts.api, "/"),
			http:    &http.Client{Timeout: 15 * time.Second},
		},
		pageStart: time.Now(),
	}
	return app.run(ctx)
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("hacker-news", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&opts.host, "host", envOr("BUSYBAR_HOST", "10.0.4.20"), "BUSY Bar hostname or IP")
	flags.StringVar(&opts.token, "token", envOr("BUSYBAR_TOKEN", ""), "Wi-Fi API token (or BUSYBAR_TOKEN)")
	flags.StringVar(&opts.api, "api", defaultAPI, "Hacker News API base URL")
	flags.DurationVar(&opts.poll, "poll", 5*time.Minute, "headline refresh interval")
	flags.DurationVar(&opts.pageDuration, "page-time", defaultPageDuration, "time to show each group of three stories")
	flags.IntVar(&opts.priority, "priority", 100, "BUSY Bar drawing priority")
	flags.BoolVar(&opts.once, "once", false, "fetch and draw once, then exit")
	flags.BoolVar(&opts.keepDisplay, "keep-display", false, "do not clear the display on exit")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Show the latest Hacker News headlines on a BUSY Bar.")
		fmt.Fprintln(flags.Output(), "\nUsage: busyctl hacker-news [flags]")
		fmt.Fprintln(flags.Output())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if opts.poll <= 0 || opts.pageDuration <= 0 {
		return opts, errors.New("poll and page-time must be positive")
	}
	return opts, nil
}

func (a *application) run(ctx context.Context) error {
	if err := a.prepareLogo(ctx); err != nil {
		return err
	}
	if err := a.refresh(ctx); err != nil {
		return err
	}
	if err := a.render(ctx, time.Now()); err != nil {
		return err
	}
	if a.options.once {
		return nil
	}

	fmt.Printf("Hacker News is active; refreshing every %s. Press Ctrl+C to stop.\n", a.options.poll)
	frameTicker := time.NewTicker(animationFrameTime)
	pollTicker := time.NewTicker(a.options.poll)
	defer frameTicker.Stop()
	defer pollTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Stopping Hacker News…")
			return nil
		case now := <-frameTicker.C:
			if err := a.render(ctx, now); err != nil {
				fmt.Fprintf(os.Stderr, "warning: display update failed: %v\n", err)
			}
		case <-pollTicker.C:
			if err := a.refresh(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: headline refresh failed: %v\n", err)
			}
		}
	}
}

func (a *application) prepareLogo(ctx context.Context) error {
	if a.assetReady {
		return nil
	}
	for frame := 0; frame < logoFrameCount; frame++ {
		payload, err := logoFramePNG(frame)
		if err != nil {
			return fmt.Errorf("render HN logo frame: %w", err)
		}
		if err := a.bar.UploadAsset(ctx, ApplicationID, logoFilename(frame), payload); err != nil {
			return fmt.Errorf("upload HN logo frame %d: %w", frame+1, err)
		}
	}
	a.assetReady = true
	return nil
}

func (a *application) refresh(ctx context.Context) error {
	stories, err := a.source.fetch(ctx, storyCount)
	if err != nil {
		return fmt.Errorf("fetch headlines: %w", err)
	}
	for index, story := range stories {
		payload, err := headlinePNG(story.Title)
		if err != nil {
			return fmt.Errorf("render headline %d: %w", index+1, err)
		}
		if err := a.bar.UploadAsset(ctx, ApplicationID, headlineFilename(index), payload); err != nil {
			return fmt.Errorf("upload headline %d: %w", index+1, err)
		}
	}
	a.stories = stories
	a.page = 0
	a.frame = 0
	a.pageStart = time.Now()
	fmt.Printf("Loaded %d Hacker News headlines.\n", len(stories))
	return nil
}

func (a *application) render(ctx context.Context, now time.Time) error {
	if len(a.stories) == 0 {
		return nil
	}
	pageCount := (len(a.stories) + visibleStories - 1) / visibleStories
	if now.Sub(a.pageStart) >= a.options.pageDuration {
		steps := int(now.Sub(a.pageStart) / a.options.pageDuration)
		a.page = (a.page + steps) % pageCount
		a.pageStart = a.pageStart.Add(time.Duration(steps) * a.options.pageDuration)
	}

	elements := make([]barapi.Element, 0, visibleStories+1)
	elapsed := now.Sub(a.pageStart)
	for row := 0; row < visibleStories; row++ {
		index := a.page*visibleStories + row
		if index >= len(a.stories) {
			break
		}
		width := tinyTextWidth(cleanText(a.stories[index].Title)) + 3
		travel := max(width+headlineGap, displayWidth-headlineX)
		offset := int(elapsed/(150*time.Millisecond)) % travel
		// Stagger the rows so the three lines feel like independent tickers.
		offset = (offset + row*9) % travel
		elements = append(elements, barapi.Element{
			ID: fmt.Sprintf("headline_%d", row), Type: "image",
			X: headlineX - offset, Y: row * 5,
			Path: headlineFilename(index), Display: "front", Opacity: intPointer(100),
		})
	}
	elements = append(elements, barapi.Element{
		ID: "hn_logo", Type: "image", X: 0, Y: 0,
		Path: logoFilename(a.frame), Display: "front", Opacity: intPointer(100),
	})

	if err := a.bar.Draw(ctx, barapi.DisplayElements{
		ApplicationName: ApplicationID,
		Priority:        a.options.priority,
		Elements:        elements,
	}); err != nil {
		return err
	}
	a.frame = (a.frame + 1) % logoFrameCount
	return nil
}

func (s sourceClient) fetch(ctx context.Context, limit int) ([]Story, error) {
	var ids []int64
	if err := s.getJSON(ctx, s.baseURL+"/topstories.json", &ids); err != nil {
		return nil, err
	}
	requestCount := min(len(ids), limit+6)
	results := make([]Story, requestCount)
	errs := make([]error, requestCount)
	semaphore := make(chan struct{}, 6)
	var wait sync.WaitGroup
	for index := 0; index < requestCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			errs[index] = s.getJSON(ctx, fmt.Sprintf("%s/item/%d.json", s.baseURL, ids[index]), &results[index])
		}(index)
	}
	wait.Wait()

	stories := make([]Story, 0, limit)
	for index, story := range results {
		if errs[index] != nil || story.Deleted || story.Dead || story.Title == "" || (story.Type != "story" && story.Type != "job") {
			continue
		}
		story.Title = cleanText(story.Title)
		stories = append(stories, story)
		if len(stories) == limit {
			break
		}
	}
	if len(stories) == 0 {
		return nil, errors.New("API returned no displayable stories")
	}
	return stories, nil
}

func (s sourceClient) getJSON(ctx context.Context, url string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "busyctl-hacker-news/0.3")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result)
}

func headlinePNG(title string) ([]byte, error) {
	title = cleanText(title)
	width := tinyTextWidth(title) + 3
	canvas := image.NewNRGBA(image.Rect(0, 0, max(width, displayWidth-headlineX), 5))
	// A hot orange two-pixel marker leads each headline into the display.
	canvas.SetNRGBA(0, 1, color.NRGBA{R: 255, G: 91, B: 24, A: 255})
	canvas.SetNRGBA(0, 2, color.NRGBA{R: 255, G: 139, B: 43, A: 255})
	canvas.SetNRGBA(0, 3, color.NRGBA{R: 255, G: 70, B: 18, A: 255})
	drawTinyText(canvas, 3, 0, title, color.NRGBA{R: 255, G: 239, B: 222, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func logoFramePNG(frame int) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, displayWidth, displayHeight))
	// The opaque left panel masks the scrolling headlines beneath the logo.
	for y := 0; y < displayHeight; y++ {
		for x := 0; x < logoWidth; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: 3, G: 2, B: 2, A: 255})
		}
	}
	for y := 1; y < 15; y++ {
		for x := 1; x < 15; x++ {
			glow := uint8(255 - y*5 - x*2)
			canvas.SetNRGBA(x, y, color.NRGBA{R: glow, G: uint8(54 + (14-y)*4), B: 12, A: 255})
		}
	}
	// Classic HN-style white Y, drawn as crisp LED pixels.
	for _, point := range [][2]int{{4, 4}, {10, 4}, {5, 5}, {9, 5}, {6, 6}, {8, 6}, {7, 7}, {7, 8}, {7, 9}, {7, 10}, {7, 11}} {
		canvas.SetNRGBA(point[0], point[1], color.NRGBA{R: 255, G: 246, B: 236, A: 255})
	}
	// Deterministic sparks drift right and fade, producing a seamless loop.
	particles := []struct{ x, y, speed int }{{13, 2, 1}, {16, 13, 2}, {21, 5, 1}, {28, 10, 2}, {39, 3, 3}, {51, 12, 2}, {63, 7, 1}}
	for index, particle := range particles {
		x := (particle.x + frame*particle.speed) % displayWidth
		if x < 15 {
			x += 15
		}
		alpha := uint8(max(45, 220-x*2))
		shade := color.NRGBA{R: 255, G: uint8(58 + (index*17)%80), B: 16, A: alpha}
		canvas.SetNRGBA(x, particle.y, shade)
		if x+1 < displayWidth && index%2 == 0 {
			shade.A /= 3
			canvas.SetNRGBA(x+1, particle.y, shade)
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func drawTinyText(destination *image.NRGBA, x, y int, value string, ink color.NRGBA) {
	for _, char := range strings.ToUpper(value) {
		glyph, ok := tinyGlyphs[char]
		if !ok {
			glyph = tinyGlyphs['?']
		}
		for row, bits := range glyph {
			for column := 0; column < 3; column++ {
				if bits&(1<<(2-column)) != 0 {
					destination.SetNRGBA(x+column, y+row, ink)
				}
			}
		}
		x += 4
	}
}

func tinyTextWidth(value string) int {
	count := utf8.RuneCountInString(value)
	if count == 0 {
		return 0
	}
	return count*4 - 1
}

var tinyGlyphs = map[rune][5]uint8{
	' ': {}, '!': {2, 2, 2, 0, 2}, '"': {5, 5}, '#': {5, 7, 5, 7, 5},
	'&': {2, 5, 2, 5, 3}, '\'': {2, 2}, '(': {2, 4, 4, 4, 2}, ')': {2, 1, 1, 1, 2},
	'*': {0, 5, 2, 5}, '+': {0, 2, 7, 2}, ',': {0, 0, 0, 2, 4}, '-': {0, 0, 7},
	'.': {0, 0, 0, 0, 2}, '/': {1, 1, 2, 4, 4}, ':': {0, 2, 0, 2}, ';': {0, 2, 0, 2, 4},
	'<': {1, 2, 4, 2, 1}, '=': {0, 7, 0, 7}, '>': {4, 2, 1, 2, 4}, '?': {6, 1, 2, 0, 2},
	'@': {2, 5, 7, 4, 3}, '[': {6, 4, 4, 4, 6}, '\\': {4, 4, 2, 1, 1}, ']': {3, 1, 1, 1, 3},
	'_': {0, 0, 0, 0, 7}, '|': {2, 2, 2, 2, 2},
	'0': {7, 5, 5, 5, 7}, '1': {2, 6, 2, 2, 7}, '2': {6, 1, 7, 4, 7}, '3': {6, 1, 3, 1, 6},
	'4': {5, 5, 7, 1, 1}, '5': {7, 4, 6, 1, 6}, '6': {3, 4, 7, 5, 7}, '7': {7, 1, 2, 2, 2},
	'8': {7, 5, 7, 5, 7}, '9': {7, 5, 7, 1, 6},
	'A': {2, 5, 7, 5, 5}, 'B': {6, 5, 6, 5, 6}, 'C': {3, 4, 4, 4, 3}, 'D': {6, 5, 5, 5, 6},
	'E': {7, 4, 6, 4, 7}, 'F': {7, 4, 6, 4, 4}, 'G': {3, 4, 5, 5, 3}, 'H': {5, 5, 7, 5, 5},
	'I': {7, 2, 2, 2, 7}, 'J': {1, 1, 1, 5, 2}, 'K': {5, 5, 6, 5, 5}, 'L': {4, 4, 4, 4, 7},
	'M': {5, 7, 7, 5, 5}, 'N': {5, 7, 7, 7, 5}, 'O': {2, 5, 5, 5, 2}, 'P': {6, 5, 6, 4, 4},
	'Q': {2, 5, 5, 3, 1}, 'R': {6, 5, 6, 5, 5}, 'S': {3, 4, 2, 1, 6}, 'T': {7, 2, 2, 2, 2},
	'U': {5, 5, 5, 5, 7}, 'V': {5, 5, 5, 5, 2}, 'W': {5, 5, 7, 7, 5}, 'X': {5, 5, 2, 5, 5},
	'Y': {5, 5, 2, 2, 2}, 'Z': {7, 1, 2, 4, 7},
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "?")
	}
	var output strings.Builder
	for _, char := range value {
		switch char {
		case '‘', '’', '‚', '‛':
			output.WriteByte('\'')
		case '“', '”', '„':
			output.WriteByte('"')
		case '—', '–', '−':
			output.WriteByte('-')
		case '…':
			output.WriteString("...")
		default:
			if char >= 0x20 && char <= 0x7e {
				output.WriteRune(char)
			} else if unicode.IsLetter(char) || unicode.IsDigit(char) {
				output.WriteByte('?')
			}
		}
	}
	return strings.Join(strings.Fields(output.String()), " ")
}

func logoFilename(frame int) string     { return fmt.Sprintf("hn-logo-%02d.png", frame) }
func headlineFilename(index int) string { return fmt.Sprintf("headline-%02d.png", index) }
func intPointer(value int) *int         { return &value }

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// Package codextokens renders the current user's local Codex token activity.
package codextokens

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
	_ "modernc.org/sqlite"
)

const (
	ApplicationID = "busybar_codex_tokens"
	ViewGraph     = "graph"
	ViewCount     = "count"
	assetPath     = "codex-tokens.png"
	defaultPoll   = 200 * time.Millisecond
	calendarWeeks = 27
	sparklineSize = 72
	displayWidth  = 72
	displayHeight = 16
)

type Config struct {
	Host         string
	Token        string
	Database     string
	Priority     int
	KeepDisplay  bool
	PollInterval time.Duration
	View         string
}

func DefaultConfig() Config {
	return Config{
		Host: envOr("BUSYBAR_HOST", "10.0.4.20"), Token: envOr("BUSYBAR_TOKEN", ""),
		Database: defaultDatabase(), Priority: 100, PollInterval: defaultPoll, View: ViewGraph,
	}
}

func (config Config) Validate() error {
	if config.Priority < 1 || config.Priority > 100 {
		return fmt.Errorf("priority %d is outside 1-100", config.Priority)
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("Codex state database path is empty")
	}
	if config.PollInterval < 100*time.Millisecond {
		return fmt.Errorf("poll interval %s is shorter than 100ms", config.PollInterval)
	}
	if config.View != ViewGraph && config.View != ViewCount {
		return fmt.Errorf("unknown token view %q (want %q or %q)", config.View, ViewGraph, ViewCount)
	}
	return nil
}

type dayUsage struct {
	Date   time.Time
	Tokens int64
}

type usage struct {
	Days      []dayUsage
	Sparkline []int64
	Today     int64
	Total     int64
}

type reader struct {
	db        *sql.DB
	now       func() time.Time
	totalOnly bool
}

func openReader(path string) (*reader, error) {
	absolute, err := filepath.Abs(expandHome(path))
	if err != nil {
		return nil, fmt.Errorf("resolve Codex database: %w", err)
	}
	if _, err := os.Stat(absolute); err != nil {
		return nil, fmt.Errorf("find Codex database at %s: %w", absolute, err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open Codex database: %w", err)
	}
	db.SetMaxOpenConns(1)
	result := &reader{db: db, now: time.Now}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("read Codex database: %w", err)
	}
	return result, nil
}

func (r *reader) close() error { return r.db.Close() }

func (r *reader) read(ctx context.Context) (usage, error) {
	if r.totalOnly {
		var total int64
		if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(tokens_used), 0) FROM threads`).Scan(&total); err != nil {
			return usage{}, fmt.Errorf("query Codex token total: %w", err)
		}
		return usage{Total: total}, nil
	}
	now := r.now().In(time.Local)
	start := calendarStart(now)
	end := start.AddDate(0, 0, calendarWeeks*7)
	values := make(map[string]int64, calendarWeeks*7)
	result := usage{}
	rows, err := r.db.QueryContext(ctx, `
		SELECT created_at, tokens_used
		FROM threads`)
	if err != nil {
		return usage{}, fmt.Errorf("query Codex token activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var created, tokens int64
		if err := rows.Scan(&created, &tokens); err != nil {
			return usage{}, fmt.Errorf("decode Codex token activity: %w", err)
		}
		result.Total += tokens
		if created < start.Unix() || created >= end.Unix() {
			continue
		}
		key := time.Unix(created, 0).In(time.Local).Format("2006-01-02")
		values[key] += tokens
	}
	if err := rows.Err(); err != nil {
		return usage{}, fmt.Errorf("read Codex token activity: %w", err)
	}

	result.Days = make([]dayUsage, 0, calendarWeeks*7)
	todayKey := now.Format("2006-01-02")
	for date := start; date.Before(end); date = date.AddDate(0, 0, 1) {
		tokens := values[date.Format("2006-01-02")]
		result.Days = append(result.Days, dayUsage{Date: date, Tokens: tokens})
		if date.Format("2006-01-02") == todayKey {
			result.Today = tokens
		}
	}
	return result, nil
}

type bar interface {
	UploadAsset(context.Context, string, string, []byte) error
	Draw(context.Context, barapi.Drawing) error
	Clear(context.Context, string) error
}

func Run(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	activity, err := openReader(config.Database)
	if err != nil {
		return err
	}
	defer activity.close()
	activity.totalOnly = config.View == ViewCount
	device := barapi.New(config.Host, config.Token)
	version, err := device.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", config.Host, err)
	}
	if err := device.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("reset BUSY Bar Codex token display: %w", err)
	}
	if !config.KeepDisplay {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = device.Clear(cleanup, ApplicationID)
		}()
	}
	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", config.Host, version.APISemver)
	fmt.Printf("Codex token %s view is reading %s every %s.\n", config.View, expandHome(config.Database), config.PollInterval)
	return run(ctx, device, activity, config)
}

type usageReader interface {
	read(context.Context) (usage, error)
}

type tokenTrend struct {
	points      [sparklineSize]int64
	lastTotal   int64
	initialized bool
}

func (trend *tokenTrend) sample(total int64) []int64 {
	delta := int64(0)
	if trend.initialized && total > trend.lastTotal {
		delta = total - trend.lastTotal
	}
	trend.initialized = true
	trend.lastTotal = total
	copy(trend.points[:], trend.points[1:])
	trend.points[len(trend.points)-1] = delta
	result := make([]int64, len(trend.points))
	copy(result, trend.points[:])
	return result
}

func run(ctx context.Context, device bar, activity usageReader, config Config) error {
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()
	lastState := ""
	lastLoggedTotal := int64(-1)
	warningActive := false
	var trend tokenTrend
	for {
		current, err := activity.read(ctx)
		if err == nil {
			if config.View == ViewCount {
				current.Sparkline = trend.sample(current.Total)
			}
			state := usageState(current)
			if state != lastState {
				err = display(ctx, device, current, config)
				if err == nil {
					if current.Total != lastLoggedTotal {
						if config.View == ViewCount {
							fmt.Printf("Codex total: %s tokens\n", formatNumber(current.Total))
						} else {
							fmt.Printf("Codex total: %s tokens (today: %s)\n", formatNumber(current.Total), formatNumber(current.Today))
						}
						lastLoggedTotal = current.Total
					}
					lastState = state
				}
			}
		}
		if err != nil && !warningActive {
			fmt.Fprintf(os.Stderr, "warning: refresh Codex tokens: %v\n", err)
		}
		warningActive = err != nil
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func display(ctx context.Context, device bar, current usage, config Config) error {
	if config.View == ViewCount {
		payload, err := renderSparkline(current.Sparkline)
		if err != nil {
			return err
		}
		if err := device.UploadAsset(ctx, ApplicationID, assetPath, payload); err != nil {
			return fmt.Errorf("upload token sparkline: %w", err)
		}
		opacity := 100
		return device.Draw(ctx, barapi.Drawing{ApplicationName: ApplicationID, Priority: config.Priority, Elements: []barapi.Element{
			{ID: "sparkline", Type: "image", X: 0, Y: 0, Path: assetPath, Display: "front", Opacity: &opacity},
			{
				ID: "count", Type: "text", X: displayWidth / 2, Y: displayHeight / 2,
				Text: formatNumber(current.Total), Font: "small", Align: "center",
				Color: "#FFFFFFFF", Display: "front",
			},
		}})
	}
	payload, err := render(current)
	if err != nil {
		return err
	}
	if err := device.UploadAsset(ctx, ApplicationID, assetPath, payload); err != nil {
		return fmt.Errorf("upload token activity: %w", err)
	}
	opacity := 100
	return device.Draw(ctx, barapi.Drawing{ApplicationName: ApplicationID, Priority: config.Priority, Elements: []barapi.Element{
		{ID: "activity", Type: "image", X: 0, Y: 0, Path: assetPath, Display: "front", Opacity: &opacity},
		{
			ID: "total", Type: "text", X: 70, Y: 8, Text: formatTokens(current.Total),
			Font: "small", Align: "mid_right", Color: "#E8F7FFFF", Display: "front",
		},
	}})
}

func renderSparkline(points []int64) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, displayWidth, displayHeight))
	values := make([]float64, sparklineSize)
	offset := sparklineSize - len(points)
	if offset < 0 {
		offset = 0
		points = points[len(points)-sparklineSize:]
	}
	for index, point := range points {
		values[offset+index] = float64(point)
	}
	smoothed := make([]float64, len(values))
	maximum := 0.0
	for index := range values {
		for distance := -7; distance <= 7; distance++ {
			source := index + distance
			if source < 0 || source >= len(values) {
				continue
			}
			absolute := distance
			if absolute < 0 {
				absolute = -absolute
			}
			weight := float64(8 - absolute)
			smoothed[index] += math.Sqrt(values[source]) * weight
		}
		if smoothed[index] > maximum {
			maximum = smoothed[index]
		}
	}
	previousY := displayHeight - 2
	for x, value := range smoothed {
		y := displayHeight - 2
		if value > 0 && maximum > 0 {
			ratio := value / maximum
			y = displayHeight - 2 - int(math.Round(ratio*float64(displayHeight-4)))
		}
		line := sparklineColor(x)
		span := displayHeight - 1 - y
		for py := y + 1; py < displayHeight-1; py++ {
			progress := float64(py-y) / float64(max(1, span))
			strength := 0.88 - 0.28*progress
			canvas.SetNRGBA(x, py, color.NRGBA{
				R: uint8(float64(line.R) * strength),
				G: uint8(float64(line.G) * strength),
				B: uint8(float64(line.B) * strength), A: 0xff,
			})
		}
		from, to := previousY, y
		if from > to {
			from, to = to, from
		}
		for py := from; py <= to; py++ {
			canvas.SetNRGBA(x, py, line)
		}
		previousY = y
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, canvas); err != nil {
		return nil, fmt.Errorf("encode token sparkline: %w", err)
	}
	return output.Bytes(), nil
}

func sparklineColor(x int) color.NRGBA {
	progress := float64(x) / float64(displayWidth-1)
	left := color.NRGBA{R: 0xa4, G: 0x78, B: 0xff, A: 0xff}
	right := color.NRGBA{R: 0x4f, G: 0xe5, B: 0xff, A: 0xff}
	return color.NRGBA{
		R: uint8(float64(left.R) + float64(int(right.R)-int(left.R))*progress),
		G: uint8(float64(left.G) + float64(int(right.G)-int(left.G))*progress),
		B: uint8(float64(left.B) + float64(int(right.B)-int(left.B))*progress),
		A: 0xff,
	}
}

func render(current usage) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, displayWidth, displayHeight))
	maxTokens := int64(0)
	for _, day := range current.Days {
		if day.Tokens > maxTokens {
			maxTokens = day.Tokens
		}
	}
	palette := []color.NRGBA{
		{R: 0x66, G: 0x79, B: 0x8a, A: 0xff},
		{R: 0x54, G: 0x9b, B: 0xcc, A: 0xff},
		{R: 0x58, G: 0xb7, B: 0xee, A: 0xff},
		{R: 0x7c, G: 0xd4, B: 0xff, A: 0xff},
		{R: 0xc4, G: 0xf0, B: 0xff, A: 0xff},
	}
	for index, day := range current.Days {
		week, weekday := index/7, index%7
		x, y := week*2, 1+weekday*2
		level := activityLevel(day.Tokens, maxTokens)
		fill(canvas, x, y, 1, 1, palette[level])
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, canvas); err != nil {
		return nil, fmt.Errorf("encode token activity: %w", err)
	}
	return output.Bytes(), nil
}
func activityLevel(tokens, maximum int64) int {
	if tokens <= 0 || maximum <= 0 {
		return 0
	}
	ratio := math.Log1p(float64(tokens)) / math.Log1p(float64(maximum))
	level := int(math.Ceil(ratio * 4))
	if level < 1 {
		return 1
	}
	if level > 4 {
		return 4
	}
	return level
}

func fill(canvas *image.NRGBA, x, y, width, height int, value color.NRGBA) {
	for py := y; py < y+height; py++ {
		for px := x; px < x+width; px++ {
			canvas.SetNRGBA(px, py, value)
		}
	}
}

func calendarStart(now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return today.AddDate(0, 0, -int(today.Weekday())-(calendarWeeks-1)*7)
}

func usageState(value usage) string {
	var result strings.Builder
	fmt.Fprintf(&result, "%d:", value.Total)
	for _, point := range value.Sparkline {
		fmt.Fprintf(&result, "%d,", point)
	}
	result.WriteByte(':')
	for _, day := range value.Days {
		fmt.Fprintf(&result, "%d,", day.Tokens)
	}
	return result.String()
}

func formatTokens(tokens int64) string {
	switch {
	case tokens < 1_000:
		return fmt.Sprintf("%d", tokens)
	case tokens < 1_000_000:
		return compact(tokens, 1_000, "K")
	case tokens < 1_000_000_000:
		return compact(tokens, 1_000_000, "M")
	case tokens < 1_000_000_000_000:
		return compact(tokens, 1_000_000_000, "B")
	default:
		return compact(tokens, 1_000_000_000_000, "T")
	}
}

func compact(tokens, unit int64, suffix string) string {
	value := float64(tokens) / float64(unit)
	if value >= 10 {
		return fmt.Sprintf("%.0f%s", value, suffix)
	}
	return fmt.Sprintf("%.1f%s", value, suffix)
}

func formatNumber(value int64) string {
	raw := fmt.Sprintf("%d", value)
	for index := len(raw) - 3; index > 0; index -= 3 {
		raw = raw[:index] + "," + raw[index:]
	}
	return raw
}

func defaultDatabase() string {
	if configured := strings.TrimSpace(os.Getenv("CODEX_STATE_DB")); configured != "" {
		return configured
	}
	base := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".codex", "state_5.sqlite")
		}
		base = filepath.Join(home, ".codex")
	}
	return filepath.Join(base, "state_5.sqlite")
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

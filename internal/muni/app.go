// Package muni renders live San Francisco Muni arrivals on a BUSY Bar.
package muni

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

const (
	ApplicationID  = "busybar_muni"
	defaultSource  = "https://webservices.umoiq.com/api/pub/v1/agencies/sfmta-cis"
	defaultLocator = "https://ipwho.is/"
	// Public browser key embedded in SFMTA's own stop pages.
	defaultAPIKey   = "0be8ebd0284ce712a63f29dcaf7798c4"
	pollInterval    = 15 * time.Second
	animationPeriod = 100 * time.Millisecond
	// Hardware treats width as a fixed text box and left-aligns within it.
	// A natural-width box anchored at 71 makes every ETA end on column 69,
	// matching the correctly positioned Now state with a two-pixel margin.
	etaRightAnchor     = 71
	LocationAuto       = "auto"
	LocationOpenAI     = "openai"
	LocationToOpenAI   = "to-openai"
	LocationFromOpenAI = "from-openai"
)

type place struct {
	line       string
	label      string
	station    string
	toward     string
	latitude   float64
	longitude  float64
	stopCodes  []string
	badgeColor color.NRGBA
	textColor  string
}

var commuteToOpenAI = []place{
	{
		line: "N", label: "Folsom", station: "The Embarcadero & Folsom St", toward: "caltrain",
		latitude: 37.79048, longitude: -122.38969, stopCodes: []string{"14509", "14510"},
		badgeColor: color.NRGBA{R: 0x00, G: 0x5b, B: 0x95, A: 0xff}, textColor: "#8FE3C7FF",
	},
	{
		line: "T", label: "4th & King", station: "4th St & King St", toward: "sunnydale",
		latitude: 37.77613, longitude: -122.39383, stopCodes: []string{"17166", "17397"},
		badgeColor: color.NRGBA{R: 0xbf, G: 0x2b, B: 0x45, A: 0xff}, textColor: "#8FE3C7FF",
	},
}

var commuteFromOpenAI = []place{
	{
		line: "T", label: "UCSF", station: "UCSF / Chase Center (16th St)", toward: "chinatown",
		latitude: 37.76850, longitude: -122.38918, stopCodes: []string{"17357", "17360"},
		badgeColor: color.NRGBA{R: 0xbf, G: 0x2b, B: 0x45, A: 0xff}, textColor: "#8FE3C7FF",
	},
	{
		line: "N", label: "4th & King", station: "King St & 4th St", toward: "ocean beach",
		latitude: 37.77627, longitude: -122.39408, stopCodes: []string{"15240"},
		badgeColor: color.NRGBA{R: 0x00, G: 0x5b, B: 0x95, A: 0xff}, textColor: "#8FE3C7FF",
	},
}

var metroLines = map[string]color.NRGBA{
	"J": {R: 0xa9, G: 0x66, B: 0x14, A: 0xff},
	"K": {R: 0x43, G: 0x7c, B: 0x93, A: 0xff},
	"L": {R: 0x94, G: 0x2d, B: 0x83, A: 0xff},
	"M": {R: 0x00, G: 0x85, B: 0x47, A: 0xff},
	"N": {R: 0x00, G: 0x5b, B: 0x95, A: 0xff},
	"T": {R: 0xbf, G: 0x2b, B: 0x45, A: 0xff},
}

type Config struct {
	Host                 string
	Token                string
	Source               string
	Locator              string
	APIKey               string
	Priority             int
	KeepDisplay          bool
	Location             string
	AllowNetworkLocation bool
}

func DefaultConfig() Config {
	return Config{
		Host: envOr("BUSYBAR_HOST", "10.0.4.20"), Token: envOr("BUSYBAR_TOKEN", ""),
		Source: defaultSource, Locator: envOr("MUNI_LOCATION_URL", defaultLocator),
		APIKey: envOr("SFMTA_API_KEY", defaultAPIKey), Priority: 100,
		Location:             envOr("MUNI_LOCATION", LocationToOpenAI),
		AllowNetworkLocation: envBool("MUNI_ALLOW_NETWORK_LOCATION"),
	}
}

func (config Config) Validate() error {
	if config.Priority < 1 || config.Priority > 100 {
		return fmt.Errorf("priority %d is outside 1-100", config.Priority)
	}
	if _, err := url.ParseRequestURI(config.Source); err != nil {
		return fmt.Errorf("invalid Muni source URL: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(config.Location), LocationAuto) {
		if !config.AllowNetworkLocation {
			return errors.New("auto location sends your IP address to the configured location service; add --allow-network-location to opt in")
		}
		if _, err := url.ParseRequestURI(config.Locator); err != nil {
			return fmt.Errorf("invalid location service URL: %w", err)
		}
		return nil
	}
	if _, _, ok := parseCoordinates(config.Location); ok {
		return nil
	}
	if _, err := selectedPlaces(config.Location); err != nil {
		return err
	}
	return nil
}

type predictionGroup struct {
	Route struct {
		ID string `json:"id"`
	} `json:"route"`
	Values []struct {
		Minutes   int   `json:"minutes"`
		Timestamp int64 `json:"timestamp"`
		Direction struct {
			Destination string `json:"destinationName"`
		} `json:"direction"`
	} `json:"values"`
}

type arrival struct {
	Minutes     int
	Timestamp   int64
	Destination string
}

type sourceClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type routeStop struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
	Name      string  `json:"name"`
	Code      string  `json:"code"`
}

type bar interface {
	UploadAsset(context.Context, string, string, []byte) error
	Draw(context.Context, barapi.Drawing) error
	Clear(context.Context, string) error
}

type application struct {
	bar       bar
	source    sourceClient
	priority  int
	selected  []place
	latest    map[string][]arrival
	pulseFrom time.Time
}

func Run(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	source := sourceClient{
		baseURL: strings.TrimRight(config.Source, "/"), apiKey: config.APIKey,
		http: &http.Client{Timeout: 10 * time.Second},
	}
	selected, err := resolvePlaces(ctx, config, source)
	if err != nil {
		return err
	}
	device := barapi.New(config.Host, config.Token)
	version, err := device.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to BUSY Bar at %s: %w", config.Host, err)
	}
	if err := device.Clear(ctx, ApplicationID); err != nil {
		return fmt.Errorf("reset BUSY Bar Muni display: %w", err)
	}
	badges := make(map[string]place)
	for _, current := range selected {
		badges[current.line] = current
	}
	for _, current := range badges {
		asset, err := routeBadge(current.line, current.badgeColor)
		if err != nil {
			return err
		}
		if err := device.UploadAsset(ctx, ApplicationID, badgePath(current.line), asset); err != nil {
			return fmt.Errorf("upload %s badge: %w", current.line, err)
		}
	}
	if !config.KeepDisplay {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = device.Clear(cleanup, ApplicationID)
		}()
	}

	app := &application{
		bar: device, priority: config.Priority, selected: selected,
		latest: make(map[string][]arrival), pulseFrom: time.Now(), source: source,
	}
	fmt.Printf("Connected to BUSY Bar at %s (API %s)\n", config.Host, version.APISemver)
	fmt.Printf("Muni is watching %s; predictions refresh every %s.\n", selectionDescription(selected), pollInterval)
	return app.run(ctx)
}

func (a *application) run(ctx context.Context) error {
	polls := time.NewTicker(pollInterval)
	animation := time.NewTicker(animationPeriod)
	defer polls.Stop()
	defer animation.Stop()
	a.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-polls.C:
			a.refresh(ctx)
		case now := <-animation.C:
			if a.shouldPulse() {
				a.render(ctx, pulseOpacity(now.Sub(a.pulseFrom)))
			}
		}
	}
}

func (a *application) refresh(ctx context.Context) {
	wasPulsing := a.shouldPulse()
	type update struct {
		line string
		data []arrival
		err  error
	}
	updates := make(chan update, len(a.selected))
	seen := make(map[string]bool)
	count := 0
	for _, current := range a.selected {
		if seen[current.line] {
			continue
		}
		seen[current.line] = true
		count++
		go func(current place) {
			data, err := a.source.fetchLine(ctx, current.line, current.stopCodes)
			if err == nil {
				data = arrivalsToward(data, current.toward)
			}
			updates <- update{line: current.line, data: data, err: err}
		}(current)
	}
	for range count {
		result := <-updates
		if result.err != nil {
			fmt.Fprintf(os.Stderr, "warning: fetch %s predictions: %v\n", result.line, result.err)
			continue
		}
		a.latest[result.line] = result.data
		fmt.Printf("%s: %s\n", result.line, arrivalLog(result.data))
	}
	now := time.Now()
	isPulsing := a.shouldPulse()
	if isPulsing && !wasPulsing {
		a.pulseFrom = now
	}
	opacity := 100
	if isPulsing {
		opacity = pulseOpacity(now.Sub(a.pulseFrom))
	}
	a.render(ctx, opacity)
}

func (a *application) render(ctx context.Context, opacity int) {
	elements := make([]barapi.Element, 0, min(2, len(a.selected))*3)
	for index, current := range a.selected[:min(2, len(a.selected))] {
		values := a.latest[current.line]
		station := current.station
		if station == "" {
			station = current.label
		}
		destination := shortStation(station)
		if len(values) != 0 {
			destination = shortDestination(values[0].Destination)
		}
		etaOpacity := 100
		if lineShouldPulse(values) {
			etaOpacity = opacity
		}
		etaAlpha := fmt.Sprintf("%02X", etaOpacity*255/100)
		etaColor := "#FFF4D6" + etaAlpha
		etaText := arrivalText(values)
		rowCenter := 8
		if len(a.selected) > 1 {
			rowCenter = index*8 + 4
		}
		suffix := fmt.Sprintf("-%d", index)
		badgeOpacity := 100
		elements = append(elements,
			barapi.Element{ID: "route" + suffix, Type: "image", X: 1, Y: rowCenter, Path: badgePath(current.line), Align: "mid_left", Display: "front", Opacity: &badgeOpacity},
			barapi.Element{ID: "destination" + suffix, Type: "text", X: 10, Y: rowCenter, Text: destination, Font: "small", Align: "mid_left", Color: current.textColor, Display: "front", Width: 43},
			barapi.Element{ID: "eta" + suffix, Type: "text", X: etaRightAnchor, Y: rowCenter, Text: etaText, Font: "small", Align: "mid_right", Color: etaColor, Display: "front"},
		)
	}
	if err := a.bar.Draw(ctx, barapi.Drawing{ApplicationName: ApplicationID, Priority: a.priority, Elements: elements}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: refresh Muni display: %v\n", err)
	}
}

func (a *application) shouldPulse() bool {
	for _, current := range a.selected {
		if lineShouldPulse(a.latest[current.line]) {
			return true
		}
	}
	return false
}

func lineShouldPulse(values []arrival) bool {
	return len(values) != 0 && values[0].Minutes <= 0
}

func pulseOpacity(elapsed time.Duration) int {
	phase := 2 * math.Pi * float64(elapsed%(1600*time.Millisecond)) / float64(1600*time.Millisecond)
	return int(math.Round(55 + 45*(0.5+0.5*math.Cos(phase))))
}

func (s sourceClient) fetchLine(ctx context.Context, line string, stops []string) ([]arrival, error) {
	type result struct {
		data []arrival
		err  error
	}
	results := make(chan result, len(stops))
	var wait sync.WaitGroup
	for _, stop := range stops {
		wait.Add(1)
		go func(stop string) {
			defer wait.Done()
			data, err := s.fetchStop(ctx, line, stop)
			results <- result{data: data, err: err}
		}(stop)
	}
	wait.Wait()
	close(results)
	var values []arrival
	var failures []error
	for result := range results {
		values = append(values, result.data...)
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	if len(failures) == len(stops) {
		return nil, errors.Join(failures...)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Timestamp < values[j].Timestamp })
	return deduplicate(values), nil
}

func arrivalsToward(values []arrival, destination string) []arrival {
	destination = strings.ToLower(strings.TrimSpace(destination))
	if destination == "" {
		return values
	}
	filtered := make([]arrival, 0, len(values))
	for _, value := range values {
		if strings.Contains(strings.ToLower(value.Destination), destination) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func (s sourceClient) fetchStop(ctx context.Context, line, stop string) ([]arrival, error) {
	endpoint := fmt.Sprintf("%s/stopcodes/%s/predictions?%s", s.baseURL, url.PathEscape(stop), url.Values{"key": {s.apiKey}}.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("stop %s returned %s", stop, response.Status)
	}
	var groups []predictionGroup
	if err := json.NewDecoder(response.Body).Decode(&groups); err != nil {
		return nil, fmt.Errorf("decode stop %s: %w", stop, err)
	}
	var values []arrival
	for _, group := range groups {
		if !strings.EqualFold(group.Route.ID, line) {
			continue
		}
		for _, value := range group.Values {
			values = append(values, arrival{Minutes: value.Minutes, Timestamp: value.Timestamp, Destination: value.Direction.Destination})
		}
	}
	return values, nil
}

func (s sourceClient) fetchRouteStops(ctx context.Context, line string) ([]routeStop, error) {
	endpoint := fmt.Sprintf("%s/routes/%s/stops?%s", s.baseURL, url.PathEscape(line), url.Values{"key": {s.apiKey}}.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("route %s stops returned %s", line, response.Status)
	}
	var stops []routeStop
	if err := json.NewDecoder(response.Body).Decode(&stops); err != nil {
		return nil, fmt.Errorf("decode route %s stops: %w", line, err)
	}
	return stops, nil
}

func resolvePlaces(ctx context.Context, config Config, source sourceClient) ([]place, error) {
	location := strings.ToLower(strings.TrimSpace(config.Location))
	if location == LocationAuto {
		latitude, longitude, err := locate(ctx, config.Locator)
		if err != nil {
			return nil, fmt.Errorf("auto-detect location: %w", err)
		}
		return source.nearestMetro(ctx, latitude, longitude)
	}
	if latitude, longitude, ok := parseCoordinates(location); ok {
		return source.nearestMetro(ctx, latitude, longitude)
	}
	return selectedPlaces(location)
}

func locate(ctx context.Context, endpoint string) (float64, float64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("location service returned %s", response.Status)
	}
	var result struct {
		Success   *bool   `json:"success"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Message   string  `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return 0, 0, fmt.Errorf("decode location: %w", err)
	}
	if result.Success != nil && !*result.Success {
		return 0, 0, errors.New(result.Message)
	}
	if result.Latitude == 0 && result.Longitude == 0 {
		return 0, 0, errors.New("location service returned no coordinates")
	}
	return result.Latitude, result.Longitude, nil
}

func (s sourceClient) nearestMetro(ctx context.Context, latitude, longitude float64) ([]place, error) {
	type routeResult struct {
		line  string
		stops []routeStop
		err   error
	}
	results := make(chan routeResult, len(metroLines))
	for line := range metroLines {
		go func(line string) {
			stops, err := s.fetchRouteStops(ctx, line)
			results <- routeResult{line: line, stops: stops, err: err}
		}(line)
	}
	all := make(map[string][]routeStop)
	var failures []error
	for range len(metroLines) {
		result := <-results
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		all[result.line] = result.stops
	}
	if len(all) == 0 {
		return nil, errors.Join(failures...)
	}
	bestLine := ""
	var best routeStop
	bestDistance := math.MaxFloat64
	for line, stops := range all {
		for _, stop := range stops {
			if stop.Code == "" {
				continue
			}
			distance := coordinateDistanceSquared(latitude, longitude, stop.Latitude, stop.Longitude)
			if distance < bestDistance {
				bestLine, best, bestDistance = line, stop, distance
			}
		}
	}
	if bestLine == "" {
		return nil, errors.New("SFMTA returned no usable Metro stops")
	}
	// IP-based auto-location is intentionally best-effort. Refuse to silently
	// choose a line when the reported point is clearly outside San Francisco.
	if bestDistance > 0.0064 {
		return nil, errors.New("reported location is too far from a Muni Metro stop")
	}
	var codes []string
	for _, stop := range all[bestLine] {
		if stop.Code != "" && coordinateDistanceSquared(best.Latitude, best.Longitude, stop.Latitude, stop.Longitude) <= 0.00000064 {
			codes = append(codes, stop.Code)
		}
	}
	if len(codes) == 0 {
		codes = []string{best.Code}
	}
	return []place{{
		line: bestLine, label: best.Name, station: best.Name, latitude: best.Latitude, longitude: best.Longitude,
		stopCodes: codes, badgeColor: metroLines[bestLine], textColor: "#8FE3C7FF",
	}}, nil
}

func selectedPlaces(location string) ([]place, error) {
	location = strings.ToLower(strings.TrimSpace(location))
	switch location {
	case "", LocationToOpenAI, "howard", "inbound":
		return append([]place(nil), commuteToOpenAI...), nil
	case LocationOpenAI, LocationFromOpenAI, "office", "outbound":
		return append([]place(nil), commuteFromOpenAI...), nil
	}
	_, _, ok := parseCoordinates(location)
	if !ok {
		return nil, fmt.Errorf("unknown location %q: use to-openai, from-openai, or LAT,LON", location)
	}
	return nil, errors.New("coordinates must be resolved before selecting a commute")
}

func parseCoordinates(value string) (float64, float64, bool) {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	latitude, latErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	longitude, lonErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if latErr != nil || lonErr != nil || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return 0, 0, false
	}
	return latitude, longitude, true
}

func coordinateDistanceSquared(latitude, longitude, targetLatitude, targetLongitude float64) float64 {
	x := (longitude - targetLongitude) * math.Cos(latitude*math.Pi/180)
	y := latitude - targetLatitude
	return x*x + y*y
}

func deduplicate(values []arrival) []arrival {
	seen := make(map[int64]bool)
	result := values[:0]
	for _, value := range values {
		key := value.Timestamp
		if key == 0 {
			key = int64(value.Minutes)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func arrivalText(values []arrival) string {
	if len(values) == 0 {
		return "Zzz"
	}
	if values[0].Minutes <= 0 {
		return "Now"
	}
	if values[0].Minutes > 99 {
		return "99+"
	}
	return fmt.Sprintf("%dm", values[0].Minutes)
}

func shortStation(value string) string {
	lower := strings.ToLower(value)
	for match, short := range map[string]string{
		"folsom": "Folsom", "ucsf": "UCSF", "4th st & king": "4th/King", "king st & 4th": "4th/King",
	} {
		if strings.Contains(lower, match) {
			return short
		}
	}
	return shortDestination(value)
}

func shortDestination(value string) string {
	lower := strings.ToLower(value)
	for match, short := range map[string]string{
		"ocean beach": "Ocean Beach", "caltrain": "Caltrain", "chinatown": "Chinatown", "sunnydale": "Sunnydale",
	} {
		if strings.Contains(lower, match) {
			return short
		}
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 10 {
		runes = runes[:10]
	}
	return string(runes)
}

func arrivalLog(values []arrival) string {
	if len(values) == 0 {
		return "no predictions"
	}
	count := min(3, len(values))
	parts := make([]string, 0, count)
	for _, value := range values[:count] {
		parts = append(parts, fmt.Sprintf("%dm to %s", value.Minutes, value.Destination))
	}
	return strings.Join(parts, ", ")
}

func badgePath(line string) string { return strings.ToLower(line) + "-badge.png" }

func routeBadge(line string, background color.NRGBA) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 7, 7))
	for y := range 7 {
		for x := range 7 {
			dx, dy := float64(x)-3, float64(y)-3
			if dx*dx+dy*dy <= 10.6 {
				canvas.SetNRGBA(x, y, background)
			}
		}
	}
	glyphs := map[string][]string{
		"J": {"011", "001", "001", "101", "010"},
		"K": {"101", "110", "100", "110", "101"},
		"L": {"100", "100", "100", "100", "111"},
		"M": {"101", "111", "101", "101", "101"},
		"N": {"101", "111", "111", "111", "101"},
		"T": {"111", "010", "010", "010", "010"},
	}
	rows, ok := glyphs[line]
	if !ok {
		return nil, fmt.Errorf("unsupported Muni badge %q", line)
	}
	glyphX := (7 - len(rows[0])) / 2
	for y, row := range rows {
		for x, pixel := range row {
			if pixel == '1' {
				canvas.SetNRGBA(x+glyphX, y+1, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
			}
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, fmt.Errorf("encode %s badge: %w", line, err)
	}
	return output.Bytes(), nil
}

func selectionDescription(selected []place) string {
	labels := make([]string, 0, len(selected))
	for _, current := range selected {
		labels = append(labels, current.line+" near "+current.label)
	}
	return strings.Join(labels, " and ")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

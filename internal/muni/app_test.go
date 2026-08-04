package muni

import (
	"context"
	"encoding/json"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	barapi "github.com/matteing/busyctl/internal/busybar"
)

type fakeBar struct {
	drawings []barapi.Drawing
}

func (fake *fakeBar) UploadAsset(context.Context, string, string, []byte) error { return nil }
func (fake *fakeBar) Clear(context.Context, string) error                       { return nil }
func (fake *fakeBar) Draw(_ context.Context, drawing barapi.Drawing) error {
	fake.drawings = append(fake.drawings, drawing)
	return nil
}

func TestDefaultConfigDoesNotTransmitLocation(t *testing.T) {
	t.Setenv("MUNI_LOCATION", "")
	config := DefaultConfig()
	if config.Location != LocationOpenAI {
		t.Fatalf("default location = %q, want openai", config.Location)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

var testNPlace = place{
	line: "N", label: "Test", station: "Test",
	badgeColor: color.NRGBA{R: 0x00, G: 0x5b, B: 0x95, A: 0xff}, textColor: "#8FE3C7FF",
}

func TestAutoLocationRequiresExplicitNetworkOptIn(t *testing.T) {
	config := DefaultConfig()
	config.Location = LocationAuto
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "--allow-network-location") {
		t.Fatalf("Validate() error = %v", err)
	}
	config.AllowNetworkLocation = true
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNearestMetroSelectsLineAndBothPlatforms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		line := strings.Split(request.URL.Path, "/")[2]
		stops := []routeStop{{Latitude: 37.70, Longitude: -122.40, Name: "Far stop", Code: "1"}}
		if line == "T" {
			stops = []routeStop{
				{Latitude: 37.76850, Longitude: -122.38918, Name: "UCSF/Chase Center", Code: "17357"},
				{Latitude: 37.76824, Longitude: -122.38937, Name: "UCSF/Chase Center", Code: "17360"},
			}
		}
		_ = json.NewEncoder(response).Encode(stops)
	}))
	defer server.Close()
	source := sourceClient{baseURL: server.URL, apiKey: "test", http: server.Client()}
	selected, err := source.nearestMetro(context.Background(), 37.7684, -122.3892)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].line != "T" {
		t.Fatalf("selected = %#v, want T", selected)
	}
	if got := strings.Join(selected[0].stopCodes, ","); got != "17357,17360" {
		t.Fatalf("stop codes = %q", got)
	}
}

func TestFetchLineCombinesDirectionsAndFiltersOtherRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		stop := strings.Split(request.URL.Path, "/")[2]
		minutes := 7
		if stop == "north" {
			minutes = 3
		}
		_, _ = response.Write([]byte(`[
			{"route":{"id":"T"},"values":[{"minutes":` + string(rune('0'+minutes)) + `,"timestamp":` + string(rune('0'+minutes)) + `000,"direction":{"destinationName":"Chinatown"}}]},
			{"route":{"id":"55"},"values":[{"minutes":1,"timestamp":1000,"direction":{"destinationName":"Dogpatch"}}]}
		]`))
	}))
	defer server.Close()
	source := sourceClient{baseURL: server.URL, apiKey: "test", http: server.Client()}
	values, err := source.fetchLine(context.Background(), "T", []string{"north", "south"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Minutes != 3 || values[1].Minutes != 7 {
		t.Fatalf("values = %#v", values)
	}
}

func TestPulseIsSmoothAndBounded(t *testing.T) {
	if got := pulseOpacity(0); got != 100 {
		t.Fatalf("pulse at start = %d, want 100", got)
	}
	if got := pulseOpacity(800 * time.Millisecond); got != 55 {
		t.Fatalf("pulse at midpoint = %d, want 55", got)
	}
	previous := pulseOpacity(0)
	for elapsed := 100 * time.Millisecond; elapsed <= 1600*time.Millisecond; elapsed += 100 * time.Millisecond {
		current := pulseOpacity(elapsed)
		if current < 55 || current > 100 || current-previous > 10 || previous-current > 10 {
			t.Fatalf("pulse jump %d -> %d at %s", previous, current, elapsed)
		}
		previous = current
	}
}

func TestTwoRowFrameUsesSharedColumnsAndIndependentPulse(t *testing.T) {
	device := &fakeBar{}
	app := application{
		bar: device, priority: 42,
		selected: []place{testNPlace, places[LocationOpenAI]},
		latest: map[string][]arrival{
			"N": {{Minutes: 3, Destination: "Ocean Beach"}},
			"T": {{Minutes: 8, Destination: "Chinatown"}},
		},
	}
	app.render(context.Background(), 55)
	if len(device.drawings) != 1 {
		t.Fatalf("draw count = %d", len(device.drawings))
	}
	drawing := device.drawings[0]
	if drawing.Priority != 42 || len(drawing.Elements) != 6 {
		t.Fatalf("drawing = %#v", drawing)
	}
	want := []struct {
		id      string
		x, y    int
		width   int
		align   string
		opacity int
		text    string
		color   string
	}{
		{id: "route-0", x: 1, y: 4, align: "mid_left", opacity: 55},
		{id: "destination-0", x: 10, y: 4, width: 43, align: "mid_left", text: "Ocean", color: "#8FE3C78C"},
		{id: "eta-0", x: 73, y: 4, width: 18, align: "mid_right", text: "3m", color: "#FFF4D68C"},
		{id: "route-1", x: 1, y: 12, align: "mid_left", opacity: 100},
		{id: "destination-1", x: 10, y: 12, width: 43, align: "mid_left", text: "China", color: "#8FE3C7FF"},
		{id: "eta-1", x: 73, y: 12, width: 18, align: "mid_right", text: "8m", color: "#FFF4D6FF"},
	}
	for index, expected := range want {
		element := drawing.Elements[index]
		if element.ID != expected.id || element.X != expected.x || element.Y != expected.y || element.Width != expected.width || element.Align != expected.align || element.Text != expected.text || element.Color != expected.color {
			t.Fatalf("element %d = %#v, want %#v", index, element, expected)
		}
		if expected.opacity != 0 && (element.Opacity == nil || *element.Opacity != expected.opacity) {
			t.Fatalf("element %d opacity = %v, want %d", index, element.Opacity, expected.opacity)
		}
	}
}

func TestMissingPredictionsShowStationAndSleepingETA(t *testing.T) {
	device := &fakeBar{}
	app := application{
		bar:      device,
		priority: 42,
		selected: []place{testNPlace, places[LocationOpenAI]},
		latest:   map[string][]arrival{},
	}
	app.render(context.Background(), 100)

	drawing := device.drawings[0]
	if drawing.Elements[1].Text != "Test" || drawing.Elements[2].Text != "Zzz" {
		t.Fatalf("N fallback = %q %q", drawing.Elements[1].Text, drawing.Elements[2].Text)
	}
	if drawing.Elements[4].Text != "UCSF" || drawing.Elements[5].Text != "Zzz" {
		t.Fatalf("T fallback = %q %q", drawing.Elements[4].Text, drawing.Elements[5].Text)
	}
}

func TestEveryETAStateUsesVisiblePixelRightAnchor(t *testing.T) {
	tests := []struct {
		name   string
		values []arrival
		text   string
	}{
		{name: "sleeping", text: "Zzz"},
		{name: "now", values: []arrival{{Minutes: 0}}, text: "NOW"},
		{name: "minutes", values: []arrival{{Minutes: 3}}, text: "3m"},
		{name: "capped", values: []arrival{{Minutes: 100}}, text: "99+"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			device := &fakeBar{}
			app := application{
				bar: device, selected: []place{testNPlace},
				latest: map[string][]arrival{"N": test.values},
			}
			app.render(context.Background(), 100)
			eta := device.drawings[0].Elements[2]
			if eta.Text != test.text || eta.X != etaRightAnchor || eta.Align != "mid_right" {
				t.Fatalf("ETA element = %#v", eta)
			}
		})
	}
}

func TestEveryMetroBadgeIsValidPNG(t *testing.T) {
	for line, background := range metroLines {
		payload, err := routeBadge(line, background)
		if err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(strings.NewReader(string(payload)))
		if err != nil {
			t.Fatalf("decode %s badge: %v", line, err)
		}
		if image.Bounds().Dx() != 7 || image.Bounds().Dy() != 7 {
			t.Fatalf("%s badge bounds = %v", line, image.Bounds())
		}
	}
}

func TestCommuteBadgesKeepInteriorPadding(t *testing.T) {
	for _, line := range []string{"N", "T"} {
		payload, err := routeBadge(line, metroLines[line])
		if err != nil {
			t.Fatal(err)
		}
		badge, err := png.Decode(strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		for _, point := range [][2]int{{1, 1}, {5, 1}, {1, 5}, {5, 5}} {
			red, green, blue, alpha := badge.At(point[0], point[1]).RGBA()
			if alpha == 0 || (red == 0xffff && green == 0xffff && blue == 0xffff) {
				t.Fatalf("%s badge has no colored padding at %v", line, point)
			}
		}
	}
}

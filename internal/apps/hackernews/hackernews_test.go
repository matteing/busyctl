package hackernews

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	barapi "github.com/matteing/busybar-apps/internal/busybar"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type fakeBar struct {
	drawings []barapi.DisplayElements
	uploads  []string
}

func (f *fakeBar) UploadAsset(_ context.Context, _, filename string, _ []byte) error {
	f.uploads = append(f.uploads, filename)
	return nil
}
func (f *fakeBar) Draw(_ context.Context, drawing barapi.DisplayElements) error {
	f.drawings = append(f.drawings, drawing)
	return nil
}
func (f *fakeBar) Clear(context.Context, string) error { return nil }

func TestSourceFetchesTopStoriesInRankOrder(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `[3,2,1]`
		if strings.Contains(request.URL.Path, "/item/") {
			id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/item/"), ".json")
			body = fmt.Sprintf(`{"id":%s,"type":"story","title":"Story %s"}`, id, id)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	source := sourceClient{baseURL: "https://example.test", http: &http.Client{Transport: transport}}
	stories, err := source.fetch(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := []int64{stories[0].ID, stories[1].ID, stories[2].ID}; fmt.Sprint(got) != "[3 2 1]" {
		t.Fatalf("story order = %v", got)
	}
}

func TestRenderShowsThreeHeadlinesAndLogo(t *testing.T) {
	t.Parallel()
	bar := &fakeBar{}
	start := time.Unix(100, 0)
	app := application{
		options:   options{priority: 100, pageDuration: 15 * time.Second},
		bar:       bar,
		stories:   []Story{{Title: "One"}, {Title: "Two"}, {Title: "Three"}, {Title: "Four"}},
		pageStart: start,
	}
	if err := app.render(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	elements := bar.drawings[0].Elements
	if len(elements) != 4 {
		t.Fatalf("element count = %d, want 4", len(elements))
	}
	for row := 0; row < 3; row++ {
		if elements[row].Y != row*5 || elements[row].Type != "image" {
			t.Fatalf("headline %d = %#v", row, elements[row])
		}
	}
	if elements[3].ID != "hn_logo" || elements[3].Path != logoFilename(0) {
		t.Fatalf("logo = %#v", elements[3])
	}
	if err := app.render(context.Background(), start.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := bar.drawings[1].Elements[0].Path; got != headlineFilename(3) {
		t.Fatalf("next page starts with %q", got)
	}
}

func TestVisualAssetsEncodeAsPNG(t *testing.T) {
	t.Parallel()
	if payload, err := headlinePNG("Launch HN: tiny headlines"); err != nil || len(payload) < 100 {
		t.Fatalf("headline PNG: %d bytes, %v", len(payload), err)
	}
	if payload, err := logoFramePNG(3); err != nil || len(payload) < 100 {
		t.Fatalf("logo PNG: %d bytes, %v", len(payload), err)
	}
}

func TestCleanTextFitsTinyFont(t *testing.T) {
	t.Parallel()
	if got := cleanText("New — faster… 🔥"); got != "New - faster..." {
		t.Fatalf("cleanText() = %q", got)
	}
}

package applemusic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	barapi "github.com/matteing/busybar-apps/internal/busybar"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeBar struct {
	drawings []barapi.DisplayElements
	uploads  [][]byte
}

func (f *fakeBar) UploadAsset(_ context.Context, _, _ string, payload []byte) error {
	f.uploads = append(f.uploads, payload)
	return nil
}

func (f *fakeBar) Draw(_ context.Context, drawing barapi.DisplayElements) error {
	f.drawings = append(f.drawings, drawing)
	return nil
}

func TestTrackNormalize(t *testing.T) {
	t.Parallel()
	track := Track{Title: "  Song   Name ", ArtistName: " The Artist "}
	track.normalize()
	if track.Name != "Song Name" || track.Artist != "The Artist" {
		t.Fatalf("unexpected normalized track: %#v", track)
	}
}

func TestCleanTextTransliteratesForBundledFont(t *testing.T) {
	t.Parallel()
	got := cleanText("Beyoncé — déjà vu… 👀🎵 東京")
	want := "Beyonce - deja vu... ? ?"
	if got != want {
		t.Fatalf("cleanText() = %q, want %q", got, want)
	}
}

func TestTrackCoverUsesNestedAlbumArtwork(t *testing.T) {
	t.Parallel()
	track := Track{Album: &Album{CoverURL: "https://example.test/album.jpg"}}
	if got := track.cover(); got != "https://example.test/album.jpg" {
		t.Fatalf("cover() = %q", got)
	}
}

func TestSourceFetch(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"track":{"id":"1","name":"Song","artist":"Artist","coverUrl":"https://example.test/cover.jpg"},
				"isPlaying":true,
				"recentAlbums":[]
			}`)),
			Request: request,
		}, nil
	})
	source := sourceClient{
		url:  "https://example.test/now-playing",
		http: &http.Client{Transport: transport},
	}
	result, err := source.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Track == nil || result.Track.Name != "Song" || !result.IsPlaying {
		t.Fatalf("unexpected now-playing response: %#v", result)
	}
}

func TestRenderUsesBundledTypography(t *testing.T) {
	t.Parallel()
	bar := &fakeBar{}
	app := application{
		options: options{priority: 100, scrollRate: 1500},
		bar:     bar,
		track:   &Track{Name: "A long song title", Artist: "A long artist name"},
	}
	if err := app.render(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(bar.drawings) != 1 {
		t.Fatalf("draw count = %d", len(bar.drawings))
	}
	if len(bar.uploads) != 0 {
		t.Fatalf("upload count = %d", len(bar.uploads))
	}
	elements := bar.drawings[0].Elements
	if len(elements) != 2 {
		t.Fatalf("element count = %d", len(elements))
	}
	if elements[0].Font != "small" || elements[0].ScrollRate != 1500 || elements[0].Width != displayWidth-2*viewPadding {
		t.Fatalf("unexpected title element: %#v", elements[0])
	}
	if elements[0].ScrollStartDelay != titleDelay || elements[0].ScrollRepeatDelay != repeatDelay {
		t.Fatalf("unexpected title timing: %#v", elements[0])
	}
	if elements[1].Font != "small" || elements[1].ScrollRate != 1500 {
		t.Fatalf("unexpected artist element: %#v", elements[1])
	}
	if elements[1].Y != 7 || elements[1].ScrollStartDelay != artistDelay {
		t.Fatalf("artist y = %d", elements[1].Y)
	}
}

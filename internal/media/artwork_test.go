package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

func TestPrepareArtwork(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 9, 9))
	colors := [3]color.NRGBA{
		{R: 240, G: 20, B: 30, A: 255},
		{R: 20, G: 220, B: 40, A: 255},
		{R: 30, G: 40, B: 230, A: 255},
	}
	for y := range 9 {
		for x := range 9 {
			source.SetNRGBA(x, y, colors[x/3])
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	useArtworkResponse(t, http.StatusOK, encoded.Bytes())

	artwork, err := PrepareArtwork("https://artwork.test/cover.png", 7)
	if err != nil {
		t.Fatalf("PrepareArtwork() error = %v", err)
	}
	assertPNGSize(t, artwork.ColorPNG, 7, 7)
	assertPNGSize(t, artwork.GrayscalePNG, 7, 7)

	grayscale, err := png.Decode(bytes.NewReader(artwork.GrayscalePNG))
	if err != nil {
		t.Fatal(err)
	}
	for y := range 7 {
		for x := range 7 {
			pixel := color.NRGBAModel.Convert(grayscale.At(x, y)).(color.NRGBA)
			if pixel.R != pixel.G || pixel.G != pixel.B {
				t.Fatalf("grayscale pixel (%d,%d) = %#v", x, y, pixel)
			}
		}
	}

	for _, expected := range colors {
		found := false
		for _, actual := range artwork.Palette {
			if channelDifference(actual.R, expected.R) < 20 &&
				channelDifference(actual.G, expected.G) < 20 &&
				channelDifference(actual.B, expected.B) < 20 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("palette %#v does not contain a color near %#v", artwork.Palette, expected)
		}
	}
}

func TestPrepareArtworkRejectsInvalidInput(t *testing.T) {
	if _, err := PrepareArtwork("http://example.invalid", 0); err == nil {
		t.Fatal("PrepareArtwork() accepted size 0")
	}
	useArtworkResponse(t, http.StatusNotFound, []byte("no"))
	if _, err := PrepareArtwork("https://artwork.test/missing.png", 16); err == nil {
		t.Fatal("PrepareArtwork() accepted non-success response")
	}
}

func TestGrayscalePNGPreservesAlpha(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 250, G: 20, B: 80, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{R: 10, G: 180, B: 240, A: 77})
	payload, err := encodePNG(source)
	if err != nil {
		t.Fatal(err)
	}

	result, err := GrayscalePNG(payload)
	if err != nil {
		t.Fatalf("GrayscalePNG() error = %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	for x, expectedAlpha := range []uint8{255, 77} {
		pixel := color.NRGBAModel.Convert(decoded.At(x, 0)).(color.NRGBA)
		if pixel.R != pixel.G || pixel.G != pixel.B || pixel.A != expectedAlpha {
			t.Errorf("pixel %d = %#v, want grayscale alpha %d", x, pixel, expectedAlpha)
		}
	}

	if _, err := GrayscalePNG([]byte("not a PNG")); err == nil {
		t.Fatal("GrayscalePNG() accepted invalid input")
	}
}

func TestSquircleMaskRoundsCornersWithoutShrinkingArtwork(t *testing.T) {
	t.Parallel()

	artwork := image.NewNRGBA(image.Rect(0, 0, 14, 14))
	for y := range 14 {
		for x := range 14 {
			artwork.SetNRGBA(x, y, color.NRGBA{R: 220, G: 40, B: 150, A: 255})
		}
	}
	applySquircleMask(artwork)

	if alpha := artwork.NRGBAAt(0, 0).A; alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}
	transition := artwork.NRGBAAt(1, 0).A
	if transition == 0 || transition == 255 {
		t.Fatalf("antialiased corner transition alpha = %d, want partial", transition)
	}
	for _, point := range []image.Point{{X: 7, Y: 0}, {X: 0, Y: 7}, {X: 7, Y: 7}, {X: 13, Y: 7}, {X: 7, Y: 13}} {
		if alpha := artwork.NRGBAAt(point.X, point.Y).A; alpha != 255 {
			t.Fatalf("artwork at %v alpha = %d, want opaque", point, alpha)
		}
	}
	if opposite := artwork.NRGBAAt(12, 0).A; opposite != transition {
		t.Fatalf("squircle is asymmetric: left alpha %d, right alpha %d", transition, opposite)
	}
}

func TestExtractPalettePrefersARealAccentOverBlackBackground(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA(image.Rect(0, 0, 48, 48))
	for y := range 48 {
		for x := range 48 {
			pixel := color.NRGBA{R: 5, G: 5, B: 7, A: 255}
			if x < 8 {
				pixel = color.NRGBA{R: 25, G: 80, B: 235, A: 255}
			}
			source.SetNRGBA(x, y, pixel)
		}
	}

	palette := extractPalette(source)
	primary := colorToOKLCH(palette[0])
	if primary.C < 0.12 || palette[0].B <= palette[0].R || palette[0].B <= palette[0].G {
		t.Fatalf("primary palette color = %#v (chroma %.3f), want the vivid blue accent", palette[0], primary.C)
	}
}

func assertPNGSize(t *testing.T, payload []byte, width, height int) {
	t.Helper()
	config, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decode PNG config: %v", err)
	}
	if config.Width != width || config.Height != height {
		t.Fatalf("PNG size = %dx%d, want %dx%d", config.Width, config.Height, width, height)
	}
}

func channelDifference(first, second uint8) int {
	difference := int(first) - int(second)
	if difference < 0 {
		return -difference
	}
	return difference
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func useArtworkResponse(t *testing.T, status int, body []byte) {
	t.Helper()
	previous := artworkClient
	artworkClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Status:     http.StatusText(status),
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() {
		artworkClient = previous
	})
}

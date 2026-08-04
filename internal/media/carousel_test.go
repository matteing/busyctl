package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestCarouselPNGsBuildsCyclicSubpixelFrames(t *testing.T) {
	t.Parallel()
	cover := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			cover.SetNRGBA(x, y, color.NRGBA{R: uint8(40 + x*40), G: uint8(y * 40), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, cover); err != nil {
		t.Fatal(err)
	}
	frames, width, err := CarouselPNGs([][]byte{encoded.Bytes()}, 2, 18, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 8 || width < 18 {
		t.Fatalf("carousel = %d frames, width %d", len(frames), width)
	}
	if bytes.Equal(frames[0], frames[1]) {
		t.Fatal("adjacent subpixel frames are identical")
	}
}

func TestTilePNGsKeepsFramesWithinDisplayWidth(t *testing.T) {
	t.Parallel()
	cover := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			cover.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, cover); err != nil {
		t.Fatal(err)
	}
	frames, _, err := CarouselPNGs([][]byte{encoded.Bytes()}, 2, 18, 2)
	if err != nil {
		t.Fatal(err)
	}
	tiles, widths, err := TilePNGs(frames, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 2 || len(widths) != 2 || widths[0] != 10 || widths[1] != 8 {
		t.Fatalf("tile dimensions = frames %d widths %v", len(tiles), widths)
	}
	for phase, phaseTiles := range tiles {
		for index, payload := range phaseTiles {
			decoded, err := png.Decode(bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() > 10 {
				t.Fatalf("phase %d tile %d width = %d", phase, index, decoded.Bounds().Dx())
			}
		}
	}
}

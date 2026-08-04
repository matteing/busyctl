package media

import (
	"bytes"
	"image/png"
	"testing"
)

func TestWaveformPNGs(t *testing.T) {
	t.Parallel()
	frames, err := WaveformPNGs(56, 16, "rgb(37, 120, 220)")
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != WaveformFrameCount {
		t.Fatalf("frame count = %d", len(frames))
	}
	image, err := png.Decode(bytes.NewReader(frames[0]))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 56 || image.Bounds().Dy() != 16 {
		t.Fatalf("frame bounds = %v", image.Bounds())
	}
	for x := range image.Bounds().Dx() {
		r, g, b, _ := image.At(x, image.Bounds().Dy()-1).RGBA()
		if r == 0 && g == 0 && b == 0 {
			t.Fatalf("column %d is not anchored to the bottom", x)
		}
	}
}

func TestSpectrumHasGranularIndependentBins(t *testing.T) {
	t.Parallel()
	first := spectrumHeights(52, 16, 0)
	next := spectrumHeights(52, 16, 3)
	unique := map[int]bool{}
	changed := 0
	for index, height := range first {
		unique[height] = true
		if height != next[index] {
			changed++
		}
	}
	if len(unique) < 6 {
		t.Fatalf("only %d distinct bar heights: %v", len(unique), first)
	}
	if changed < len(first)/3 {
		t.Fatalf("only %d of %d bins animate independently", changed, len(first))
	}
}

func TestSpectrumLoopIsSeamless(t *testing.T) {
	t.Parallel()
	first := spectrumHeights(52, 16, 0)
	wrapped := spectrumHeights(52, 16, WaveformFrameCount)
	for index := range first {
		if first[index] != wrapped[index] {
			t.Fatalf("wrapped bin %d = %d, want %d", index, wrapped[index], first[index])
		}
	}
}

func TestSpectrumMotionHasNoAbruptFrame(t *testing.T) {
	t.Parallel()
	previous := spectrumHeights(52, 16, 0)
	for frame := 1; frame <= WaveformFrameCount; frame++ {
		current := spectrumHeights(52, 16, frame)
		for bin := range current {
			jump := current[bin] - previous[bin]
			if jump < 0 {
				jump = -jump
			}
			if jump > 2 {
				t.Fatalf("frame %d bin %d jumps %d pixels (%d -> %d)", frame, bin, jump, previous[bin], current[bin])
			}
		}
		previous = current
	}
}

func TestParseColor(t *testing.T) {
	t.Parallel()
	got := parseColor("rgb(12, 34, 56)")
	if got.R != 12 || got.G != 34 || got.B != 56 {
		t.Fatalf("parsed color = %#v", got)
	}
}

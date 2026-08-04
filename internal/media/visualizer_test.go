package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func TestGenerateVisualizerPNGs(t *testing.T) {
	t.Parallel()

	palette := [3]color.NRGBA{
		{R: 215, G: 45, B: 70, A: 255},
		{R: 95, G: 70, B: 220, A: 255},
		{R: 35, G: 170, B: 235, A: 255},
	}
	frames, err := GenerateVisualizerPNGs(55, 14, palette)
	if err != nil {
		t.Fatalf("GenerateVisualizerPNGs() error = %v", err)
	}
	if len(frames) != VisualizerFrameCount {
		t.Fatalf("frame count = %d, want %d", len(frames), VisualizerFrameCount)
	}

	heights := make([][]int, len(frames))
	for frameIndex, payload := range frames {
		decoded, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("decode frame %d: %v", frameIndex, err)
		}
		if decoded.Bounds() != image.Rect(0, 0, 55, 14) {
			t.Fatalf("frame %d bounds = %v", frameIndex, decoded.Bounds())
		}
		heights[frameIndex] = barHeights(decoded)
	}

	changed := false
	maximumInteriorStep := 0
	tallestPeak := 0
	for frameIndex := 1; frameIndex < len(heights); frameIndex++ {
		step := maximumHeightDifference(heights[frameIndex-1], heights[frameIndex])
		maximumInteriorStep = max(maximumInteriorStep, step)
		if step > 0 {
			changed = true
		}
		for _, height := range heights[frameIndex] {
			tallestPeak = max(tallestPeak, height)
		}
	}
	if !changed {
		t.Fatal("visualizer frames do not animate")
	}
	boundaryStep := maximumHeightDifference(heights[len(heights)-1], heights[0])
	if boundaryStep > maximumInteriorStep {
		t.Fatalf("loop boundary step = %d, larger than interior maximum %d", boundaryStep, maximumInteriorStep)
	}
	if maximumInteriorStep > 1 {
		t.Fatalf("adjacent-frame height step = %d, want <= 1 for smooth motion", maximumInteriorStep)
	}
	if tallestPeak != 14 {
		t.Fatalf("tallest peak = %d, want occasional full-height spectrum peaks", tallestPeak)
	}
}

func TestVisualizerBarsAreBottomAnchoredGradients(t *testing.T) {
	t.Parallel()

	frames, err := GenerateVisualizerPNGs(24, 14, [3]color.NRGBA{
		{R: 240, G: 30, B: 20, A: 255},
		{R: 30, G: 230, B: 40, A: 255},
		{R: 25, G: 45, B: 245, A: 255},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(frames[0]))
	if err != nil {
		t.Fatal(err)
	}
	for y := range 14 {
		for x := range 24 {
			pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if pixel.A != 255 {
				t.Fatalf("pixel (%d,%d) alpha = %d, want opaque frame", x, y, pixel.A)
			}
		}
	}

	for x, height := range barHeights(decoded) {
		if height == 0 {
			t.Fatalf("column %d has no bar", x)
		}
		bottom := color.NRGBAModel.Convert(decoded.At(x, 13)).(color.NRGBA)
		if isBlack(bottom) {
			t.Fatalf("column %d does not reach the bottom", x)
		}
		for y := 14 - height; y < 14; y++ {
			if isBlack(color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)) {
				t.Fatalf("column %d has a gap at y=%d", x, y)
			}
		}
	}

	heights := barHeights(decoded)
	tallestX := 0
	for x := range heights {
		if heights[x] > heights[tallestX] {
			tallestX = x
		}
	}
	topY := 14 - heights[tallestX]
	top := color.NRGBAModel.Convert(decoded.At(tallestX, topY)).(color.NRGBA)
	bottom := color.NRGBAModel.Convert(decoded.At(tallestX, 13)).(color.NRGBA)
	if contrast := relativeLuminance(top) - relativeLuminance(bottom); contrast < 0.45 {
		t.Fatalf("bar luminance contrast = %.3f, want a clearly visible gradient; top=%#v bottom=%#v", contrast, top, bottom)
	}
}

func TestVisualizerThemeIsVividMonotonicAndUsesThePalette(t *testing.T) {
	t.Parallel()

	theme := makeVisualizerTheme(55, 14, [3]color.NRGBA{
		{R: 235, G: 45, B: 90, A: 255},
		{R: 40, G: 90, B: 235, A: 255},
		{R: 245, G: 185, B: 25, A: 255},
	})
	for x := range theme.body {
		previous := relativeLuminance(theme.body[x][13])
		for y := 12; y >= 0; y-- {
			current := relativeLuminance(theme.body[x][y])
			if current+0.005 < previous {
				t.Fatalf("column %d gradient darkened at y=%d: %.3f -> %.3f", x, y, previous, current)
			}
			previous = current
		}
		if delta := relativeLuminance(theme.body[x][0]) - relativeLuminance(theme.body[x][13]); delta < 0.55 {
			t.Fatalf("column %d gradient delta = %.3f, want >= .55", x, delta)
		}
	}

	bottom := colorToOKLCH(theme.body[0][13])
	top := colorToOKLCH(theme.body[0][0])
	if hueDistance := math.Abs(shortestHueDelta(bottom.H, top.H)); hueDistance < 0.25 {
		t.Fatalf("body gradient did not use distinct palette hues: bottom=%#v top=%#v", theme.body[0][13], theme.body[0][0])
	}
	for x, cap := range theme.cap {
		if chroma := colorToOKLCH(cap).C; chroma < 0.07 {
			t.Fatalf("cap %d is pasty (chroma %.3f): %#v", x, chroma, cap)
		}
	}
}

func TestNeutralArtworkProducesTintedSilverNotAnInventedHue(t *testing.T) {
	t.Parallel()

	theme := makeVisualizerTheme(12, 14, [3]color.NRGBA{
		{R: 45, G: 45, B: 45, A: 255},
		{R: 125, G: 125, B: 125, A: 255},
		{R: 220, G: 220, B: 220, A: 255},
	})
	for x, cap := range theme.cap {
		maximum := max(cap.R, cap.G, cap.B)
		minimum := min(cap.R, cap.G, cap.B)
		if maximum-minimum > 3 {
			t.Fatalf("neutral cap %d invented a hue: %#v", x, cap)
		}
	}
}

func TestGenerateVisualizerPNGsRejectsInvalidDimensions(t *testing.T) {
	t.Parallel()

	if _, err := GenerateVisualizerPNGs(0, 16, [3]color.NRGBA{}); err == nil {
		t.Fatal("accepted zero width")
	}
	if _, err := GenerateVisualizerPNGs(16, -1, [3]color.NRGBA{}); err == nil {
		t.Fatal("accepted negative height")
	}
}

func barHeights(source image.Image) []int {
	bounds := source.Bounds()
	heights := make([]int, bounds.Dx())
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		for y := bounds.Max.Y - 1; y >= bounds.Min.Y; y-- {
			pixel := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			if isBlack(pixel) {
				break
			}
			heights[x-bounds.Min.X]++
		}
	}
	return heights
}

func maximumHeightDifference(first, second []int) int {
	maximum := 0
	for index := range first {
		difference := first[index] - second[index]
		if difference < 0 {
			difference = -difference
		}
		maximum = max(maximum, difference)
	}
	return maximum
}

func isBlack(pixel color.NRGBA) bool {
	return pixel.R == 0 && pixel.G == 0 && pixel.B == 0
}

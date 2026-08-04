package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"sort"
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
	if maximumInteriorStep > 2 {
		t.Fatalf("adjacent-frame height step = %d, want <= 2 for responsive smooth motion", maximumInteriorStep)
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
	tallestX := min(3, len(heights)-1)
	for x := tallestX; x < max(tallestX+1, len(heights)-3); x++ {
		if heights[x] > heights[tallestX] {
			tallestX = x
		}
	}
	topY := 14 - heights[tallestX]
	bottom := color.NRGBAModel.Convert(decoded.At(tallestX, 13)).(color.NRGBA)
	brightest := color.NRGBA{}
	for y := topY; y < 14; y++ {
		candidate := color.NRGBAModel.Convert(decoded.At(tallestX, y)).(color.NRGBA)
		if relativeLuminance(candidate) > relativeLuminance(brightest) {
			brightest = candidate
		}
	}
	if contrast := relativeLuminance(brightest) - relativeLuminance(bottom); contrast < 0.35 {
		t.Fatalf("bar luminance contrast = %.3f, want a clearly visible gradient; brightest=%#v bottom=%#v", contrast, brightest, bottom)
	}
}

func TestVisualizerEdgeFadeEmergesSmoothlyFromTheWaveformBounds(t *testing.T) {
	t.Parallel()

	left := []float64{
		visualizerEdgeFade(0, 7, 21, 15),
		visualizerEdgeFade(1, 7, 21, 15),
		visualizerEdgeFade(2, 7, 21, 15),
		visualizerEdgeFade(3, 7, 21, 15),
	}
	for index := 1; index < len(left); index++ {
		if left[index] <= left[index-1] {
			t.Fatalf("left fade is not progressive: %v", left)
		}
	}
	if left[len(left)-1] != 1 {
		t.Fatalf("interior fade = %.3f, want 1", left[len(left)-1])
	}
	if right := visualizerEdgeFade(20, 7, 21, 15); right != left[0] {
		t.Fatalf("right edge fade = %.3f, want symmetric %.3f", right, left[0])
	}
	top := visualizerEdgeFade(10, 0, 21, 15)
	bottom := visualizerEdgeFade(10, 14, 21, 15)
	if !(top < bottom && bottom < 1) {
		t.Fatalf("top/bottom fades = %.3f/%.3f, want a gentler but visible bottom fade", top, bottom)
	}

	source := color.NRGBA{R: 230, G: 45, B: 170, A: 255}
	edge := fadeVisualizerEdge(source, 0, 7, 21, 15)
	interior := fadeVisualizerEdge(source, 10, 7, 21, 15)
	if edge.A != 255 || interior != source {
		t.Fatalf("edge fade changed opacity or interior color: edge=%#v interior=%#v", edge, interior)
	}
	if relativeLuminance(edge) >= relativeLuminance(interior) {
		t.Fatalf("edge luminance %.3f did not fade below interior %.3f", relativeLuminance(edge), relativeLuminance(interior))
	}
	if chroma := colorToOKLCH(edge).C; chroma < 0.05 {
		t.Fatalf("edge lost its color while fading: chroma %.3f, color %#v", chroma, edge)
	}
}

func TestVisualizerSpectrumHasDynamicValleysAndDiscretePeaks(t *testing.T) {
	t.Parallel()

	palettes := [][3]color.NRGBA{
		{{R: 215, G: 45, B: 70, A: 255}, {R: 95, G: 70, B: 220, A: 255}, {R: 35, G: 170, B: 235, A: 255}},
		{{R: 230, G: 120, B: 30, A: 255}, {R: 30, G: 175, B: 115, A: 255}, {R: 245, G: 210, B: 90, A: 255}},
		{{R: 60, G: 35, B: 150, A: 255}, {R: 210, G: 45, B: 155, A: 255}, {R: 75, G: 150, B: 235, A: 255}},
	}
	for paletteIndex, palette := range palettes {
		paletteIndex, palette := paletteIndex, palette
		t.Run(fmt.Sprintf("palette_%d", paletteIndex), func(t *testing.T) {
			t.Parallel()

			frames := generateSpectrumHeights(55, 14, palette)
			lowSamples := 0
			columnsThatWentLow := make([]bool, 55)
			framesWithSeveralPeaks := 0
			framesWithWideRange := 0
			framesWithBroadClump := 0
			totalVariation := 0
			for _, heights := range frames {
				minimumHeight, maximumHeight := heights[0], heights[0]
				for x, barHeight := range heights {
					minimumHeight = min(minimumHeight, barHeight)
					maximumHeight = max(maximumHeight, barHeight)
					if barHeight <= 3 {
						lowSamples++
						columnsThatWentLow[x] = true
					}
					if x > 0 {
						totalVariation += absInt(barHeight - heights[x-1])
					}
				}
				if maximumHeight-minimumHeight >= 4 {
					framesWithWideRange++
				}
				if prominentPeakCount(heights) >= 3 {
					framesWithSeveralPeaks++
				}
				if longestHighRun(heights) > 10 {
					framesWithBroadClump++
				}
			}

			lowColumns := 0
			for _, wentLow := range columnsThatWentLow {
				if wentLow {
					lowColumns++
				}
			}
			totalSamples := len(frames) * len(frames[0])
			if lowSamples*20 < totalSamples {
				t.Fatalf("only %.1f%% of bars reached <=3px; want visible valleys", 100*float64(lowSamples)/float64(totalSamples))
			}
			if lowColumns*10 < len(columnsThatWentLow)*7 {
				t.Fatalf("only %d/%d columns reached <=3px", lowColumns, len(columnsThatWentLow))
			}
			if framesWithSeveralPeaks*10 < len(frames)*6 {
				t.Fatalf("only %d/%d frames had at least three prominent peaks", framesWithSeveralPeaks, len(frames))
			}
			if framesWithWideRange*10 < len(frames)*6 {
				t.Fatalf("only %d/%d frames had >=4px spatial range", framesWithWideRange, len(frames))
			}
			if framesWithBroadClump*10 > len(frames) {
				t.Fatalf("%d/%d frames contained a >10-column high clump", framesWithBroadClump, len(frames))
			}
			averageVariation := float64(totalVariation) / float64(len(frames)*(len(frames[0])-1))
			if averageVariation < 0.55 {
				t.Fatalf("average adjacent-bar variation = %.2f, silhouette is too uniform", averageVariation)
			}
		})
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
		if delta := relativeLuminance(theme.body[x][0]) - relativeLuminance(theme.body[x][13]); delta < 0.40 {
			t.Fatalf("column %d gradient delta = %.3f, want >= .40", x, delta)
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

func prominentPeakCount(heights []int) int {
	count := 0
	for x := 2; x+2 < len(heights); x++ {
		leftMinimum := min(heights[x-2], heights[x-1])
		rightMinimum := min(heights[x+1], heights[x+2])
		if heights[x] >= 6 && heights[x] >= heights[x-1] && heights[x] >= heights[x+1] &&
			heights[x]-max(leftMinimum, rightMinimum) >= 2 {
			count++
		}
	}
	return count
}

func longestHighRun(heights []int) int {
	ordered := append([]int(nil), heights...)
	sort.Ints(ordered)
	threshold := ordered[len(ordered)/2] + 2
	longest, current := 0, 0
	for _, height := range heights {
		if height >= threshold {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func isBlack(pixel color.NRGBA) bool {
	return pixel.R == 0 && pixel.G == 0 && pixel.B == 0
}

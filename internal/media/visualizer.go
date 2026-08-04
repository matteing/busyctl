package media

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

const (
	VisualizerFPS        = 15
	VisualizerFrameCount = 90
	maxVisualizerPixels  = 128_000_000
)

// GenerateVisualizerPNGs creates a six-second seamless spectrum animation.
// Each x coordinate is one independently moving frequency bar. Complete,
// opaque frames and one fixed image element keep the firmware render path
// deterministic: there are no moving elements, transparent trails, or queued
// subframes.
func GenerateVisualizerPNGs(width, height int, palette [3]color.NRGBA) ([][]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid visualizer dimensions %dx%d", width, height)
	}
	if width > 4096 || height > 4096 || int64(width)*int64(height)*VisualizerFrameCount > maxVisualizerPixels {
		return nil, fmt.Errorf("visualizer dimensions %dx%d are too large", width, height)
	}

	theme := makeVisualizerTheme(width, height, palette)
	heightFrames := make([][]int, VisualizerFrameCount)
	for frameIndex := range VisualizerFrameCount {
		phase := 2 * math.Pi * float64(frameIndex) / VisualizerFrameCount
		heightFrames[frameIndex] = visualizerBarHeights(width, height, phase)
	}
	limitVisualizerMotion(heightFrames)

	frames := make([][]byte, VisualizerFrameCount)
	for frameIndex := range VisualizerFrameCount {
		frame := image.NewNRGBA(image.Rect(0, 0, width, height))
		for alpha := 3; alpha < len(frame.Pix); alpha += 4 {
			frame.Pix[alpha] = 255
		}

		for x, barHeight := range heightFrames[frameIndex] {
			top := height - barHeight
			for y := top; y < height; y++ {
				frame.SetNRGBA(x, y, theme.body[x][y])
			}
			// A bright album-colored cap gives the silhouette the crisp dotted
			// edge of a real spectrum analyzer instead of a soft rolling wave.
			frame.SetNRGBA(x, top, theme.cap[x])
		}

		encoded, err := encodePNG(frame)
		if err != nil {
			return nil, fmt.Errorf("encode visualizer frame %d: %w", frameIndex, err)
		}
		frames[frameIndex] = encoded
	}
	return frames, nil
}

// limitVisualizerMotion projects the periodic height sequence onto a strict
// one-pixel-per-frame velocity limit. It only trims peaks that are too narrow,
// retaining broad transients while guaranteeing a smooth loop boundary.
func limitVisualizerMotion(frames [][]int) {
	if len(frames) < 2 {
		return
	}
	for {
		changed := false
		for frameIndex := range frames {
			nextIndex := (frameIndex + 1) % len(frames)
			for x := range frames[frameIndex] {
				if frames[nextIndex][x] > frames[frameIndex][x]+1 {
					frames[nextIndex][x] = frames[frameIndex][x] + 1
					changed = true
				}
				if frames[frameIndex][x] > frames[nextIndex][x]+1 {
					frames[frameIndex][x] = frames[nextIndex][x] + 1
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

func visualizerBarHeights(width, height int, phase float64) []int {
	raw := make([]float64, width)
	for x := range width {
		position := 0.0
		if width > 1 {
			position = float64(x) / float64(width-1)
		}
		base := 0.25 + 0.07*(1-position) + 0.07*(visualizerNoise(x, 0)-0.5) +
			0.10*math.Pow(visualizerNoise(x, 5), 7)
		breath := 0.075 * math.Sin(2*phase+2*math.Pi*visualizerNoise(x, 1))
		flutter := 0.028 * math.Sin(5*phase+2*math.Pi*visualizerNoise(x, 2))
		peak := 0.25 * math.Pow(0.5+0.5*math.Sin(2*phase+2*math.Pi*visualizerNoise(x, 3)), 8)
		transient := 0.065 * math.Pow(0.5+0.5*math.Sin(5*phase+2*math.Pi*visualizerNoise(x, 4)), 10)
		raw[x] = clamp(base+breath+flutter+peak+transient, 0.06, 1)
	}

	heights := make([]int, width)
	for x := range width {
		previous := raw[max(0, x-1)]
		next := raw[min(width-1, x+1)]
		// A small amount of neighbor correlation makes clusters feel musical,
		// while retaining enough independent motion to avoid a traveling wave.
		level := 0.72*raw[x] + 0.14*previous + 0.14*next
		normalized := clamp((level-0.08)/0.65, 0, 1)
		heights[x] = min(height, 2+int(math.Round(normalized*float64(max(0, height-2)))))
	}
	return heights
}

type visualizerTheme struct {
	body [][]color.NRGBA
	cap  []color.NRGBA
}

func makeVisualizerTheme(width, height int, palette [3]color.NRGBA) visualizerTheme {
	colors := [3]oklchColor{
		colorToOKLCH(palette[0]),
		colorToOKLCH(palette[1]),
		colorToOKLCH(palette[2]),
	}
	primaryIndex := 0
	bestScore := -1.0
	for index, candidate := range colors {
		readability := math.Exp(-math.Pow((candidate.L-0.60)/0.30, 2))
		score := 4*candidate.C + 0.2*readability
		if score > bestScore {
			primaryIndex, bestScore = index, score
		}
	}
	if primaryIndex != 0 {
		colors[0], colors[primaryIndex] = colors[primaryIndex], colors[0]
	}

	neutral := colors[0].C < 0.04
	primaryChroma := clamp(colors[0].C*1.15, 0.105, 0.19)
	if neutral {
		primaryChroma = min(0.045, colors[0].C*1.35)
	}
	for index := 1; index < len(colors); index++ {
		if colors[index].C < 0.025 {
			colors[index].H = colors[0].H
		}
	}
	bottomHue := colors[1].H
	topHue := colors[2].H
	if neutral {
		bottomHue, topHue = colors[0].H, colors[0].H
	}

	theme := visualizerTheme{
		body: make([][]color.NRGBA, width),
		cap:  make([]color.NRGBA, width),
	}
	for x := range width {
		theme.body[x] = make([]color.NRGBA, height)
		for y := range height {
			position := 0.0
			if height > 1 {
				position = float64(height-1-y) / float64(height-1)
			}
			lightness := 0.20 + 0.72*math.Pow(position, 0.84)
			chroma := primaryChroma * (1 - 0.52*position)
			hue := normalizeHue(bottomHue + shortestHueDelta(bottomHue, topHue)*position)
			theme.body[x][y] = oklchToColor(oklchColor{L: lightness, C: chroma, H: hue})
		}

		capChroma := clamp(colors[0].C*1.12, 0.12, 0.20)
		if neutral {
			capChroma = min(0.055, colors[0].C*1.35)
		}
		theme.cap[x] = oklchToColor(oklchColor{L: 0.79, C: capChroma, H: colors[0].H})
	}
	return theme
}

func visualizerNoise(index, salt int) float64 {
	value := uint32(index+1)*0x9e3779b9 ^ uint32(salt+1)*0x85ebca6b
	value ^= value >> 16
	value *= 0x7feb352d
	value ^= value >> 15
	value *= 0x846ca68b
	value ^= value >> 16
	return float64(value) / float64(math.MaxUint32)
}

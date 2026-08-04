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
	heightFrames := generateSpectrumHeights(width, height, palette)

	frames := make([][]byte, VisualizerFrameCount)
	for frameIndex := range VisualizerFrameCount {
		frame := image.NewNRGBA(image.Rect(0, 0, width, height))
		for alpha := 3; alpha < len(frame.Pix); alpha += 4 {
			frame.Pix[alpha] = 255
		}

		for x, barHeight := range heightFrames[frameIndex] {
			top := height - barHeight
			for y := top; y < height; y++ {
				frame.SetNRGBA(x, y, fadeVisualizerEdge(theme.body[x][y], x, y, width, height))
			}
			// A bright album-colored cap gives the silhouette the crisp dotted
			// edge of a real spectrum analyzer instead of a soft rolling wave.
			frame.SetNRGBA(x, top, fadeVisualizerEdge(theme.cap[x], x, top, width, height))
		}

		encoded, err := encodePNG(frame)
		if err != nil {
			return nil, fmt.Errorf("encode visualizer frame %d: %w", frameIndex, err)
		}
		frames[frameIndex] = encoded
	}
	return frames, nil
}

// fadeVisualizerEdge applies a small perceptual vignette inside the waveform
// rectangle. The album art remains untouched, and the bottom stays at full
// brightness so the spectrum remains firmly grounded on the display edge.
func fadeVisualizerEdge(source color.NRGBA, x, y, width, height int) color.NRGBA {
	fade := visualizerEdgeFade(x, y, width, height)
	if fade >= 1 {
		return source
	}
	converted := colorToOKLCH(source)
	converted.L *= fade
	// Preserve most of the chroma while the lightness recedes into black. This
	// keeps the edge colorful instead of turning it into a gray fringe.
	converted.C *= 0.78 + 0.22*fade
	return oklchToColor(converted)
}

func visualizerEdgeFade(x, y, width, height int) float64 {
	if width <= 0 || height <= 0 {
		return 0
	}
	horizontalDistance := float64(min(x, width-1-x))
	topDistance := float64(y)
	horizontal := 0.28 + 0.72*smoothstep(0, 3, horizontalDistance)
	top := 0.28 + 0.72*smoothstep(0, 3, topDistance)
	return min(horizontal, top)
}

type spectrumEvent struct {
	frame     int
	center    float64
	spread    float64
	amplitude float64
	attack    int
	decay     int
}

func generateSpectrumHeights(width, height int, palette [3]color.NRGBA) [][]int {
	random := newSpectrumRandom(palette)
	events := makeSpectrumEvents(width, &random)
	levels := make([][]float64, VisualizerFrameCount)
	for frameIndex := range levels {
		levels[frameIndex] = make([]float64, width)
		phase := 2 * math.Pi * float64(frameIndex) / VisualizerFrameCount
		// The loop breathes between a fuller, connected spectrum and a more
		// open transient passage. This avoids locking the visualizer into either
		// a solid curtain or a field of unrelated spikes.
		fullness := spectrumFullness(phase)
		for x := range width {
			position := 0.0
			if width > 1 {
				position = float64(x) / float64(width-1)
			}
			// A low pink-spectrum noise floor leaves room for visible one- and
			// two-pixel valleys. Two- or three-bin groups provide the coherence
			// of a spectrum analyser, while each bin still gets its own faster
			// articulation so the silhouette never becomes one smooth mound.
			level := 0.025 + 0.035*math.Pow(1-position, 0.70)
			level += 0.018 * (visualizerNoise(x, 42) - 0.5)
			bedShape := 0.84 + 0.10*math.Sin(4*math.Pi*position+0.35*phase) +
				0.06*math.Sin(10*math.Pi*position-0.20*phase)
			level += (0.022 + 0.078*fullness) * bedShape
			cluster := x / 3
			clusterEnergy := spectrumPulse(cluster, phase, 50, 5, 5, 9)
			detailEnergy := spectrumPulse(x, phase, 61, 8, 9, 12)
			sparkEnergy := spectrumPulse(x, phase, 72, 15, 11, 18)
			clusterAmount := 0.22 - 0.06*position
			detailAmount := 0.21 - 0.045*position
			level += clusterAmount*clusterEnergy + detailAmount*detailEnergy
			if position > 0.52 {
				level += 0.14 * smoothstep(0.52, 1, position) * sparkEnergy
			}

			// Musical events are intentionally only a few bins wide. Taking the
			// strongest event (plus a trace of the remainder) makes kick, snare,
			// hat, and melodic accents read as separate peaks instead of summing
			// into a solid block across the display.
			eventPeak := 0.0
			eventBed := 0.0
			for _, event := range events {
				timeEnergy := spectrumEventEnvelope(frameIndex, event)
				if timeEnergy == 0 {
					continue
				}
				distance := (float64(x) - event.center) / event.spread
				energy := event.amplitude * timeEnergy * math.Exp(-0.5*distance*distance)
				eventPeak = max(eventPeak, energy)
				eventBed += energy
			}
			level += eventPeak + 0.08*min(eventBed, 1.0)
			levels[frameIndex][x] = clamp(level, 0, 1)
		}
	}

	// Neighbor correlation follows the passage fullness: active passages share
	// enough energy to form a connected spectrum, while quieter passages retain
	// the local separation and valleys of individual frequency bins.
	spatial := make([][]float64, VisualizerFrameCount)
	for frameIndex := range spatial {
		spatial[frameIndex] = make([]float64, width)
		phase := 2 * math.Pi * float64(frameIndex) / VisualizerFrameCount
		fullness := spectrumFullness(phase)
		nearWeight := 0.055 + 0.040*fullness
		farWeight := 0.010 + 0.018*fullness
		centerWeight := 1 - 2*nearWeight - 2*farWeight
		for x := range width {
			spatial[frameIndex][x] = farWeight*levels[frameIndex][max(0, x-2)] +
				nearWeight*levels[frameIndex][max(0, x-1)] +
				centerWeight*levels[frameIndex][x] +
				nearWeight*levels[frameIndex][min(width-1, x+1)] +
				farWeight*levels[frameIndex][min(width-1, x+2)]
		}
	}

	// A light circular temporal filter removes synthetic one-frame impulses
	// without erasing the faster hi-hat response.
	targets := make([][]int, VisualizerFrameCount)
	for frameIndex := range targets {
		targets[frameIndex] = make([]int, width)
		previous := (frameIndex + VisualizerFrameCount - 1) % VisualizerFrameCount
		next := (frameIndex + 1) % VisualizerFrameCount
		for x := range width {
			level := 0.10*spatial[previous][x] + 0.80*spatial[frameIndex][x] + 0.10*spatial[next][x]
			normalized := clamp((level-0.025)/0.58, 0, 1)
			targets[frameIndex][x] = min(height, 1+int(math.Round(math.Pow(normalized, 0.92)*float64(max(0, height-1)))))
		}
	}
	return followSpectrumTargets(targets, height)
}

func spectrumFullness(phase float64) float64 {
	energy := clamp(0.50+
		0.30*math.Sin(2*phase+2*math.Pi*visualizerNoise(0, 83))+
		0.20*math.Sin(3*phase+2*math.Pi*visualizerNoise(0, 84)), 0, 1)
	return smoothstep(0.08, 0.92, energy)
}

func spectrumPulse(index int, phase float64, salt, minimumFrequency, frequencySpread int, power float64) float64 {
	frequency := minimumFrequency + int(visualizerNoise(index, salt)*float64(frequencySpread))
	offset := 2 * math.Pi * visualizerNoise(index, salt+7)
	return math.Pow(0.5+0.5*math.Sin(float64(frequency)*phase+offset), power)
}

func makeSpectrumEvents(width int, random *spectrumRandom) []spectrumEvent {
	beatCount := 10 + random.integer(4) // 100, 110, 120, or 130 BPM.
	frameAt := func(beat float64) int {
		return int(math.Round(beat*VisualizerFrameCount/float64(beatCount))) % VisualizerFrameCount
	}
	barAt := func(position float64) float64 {
		return position * float64(max(0, width-1))
	}
	events := make([]spectrumEvent, 0, beatCount*4+8)
	for beat := range beatCount {
		// Kicks are coherent in a tight low-frequency cluster. Occasional
		// deterministic omissions keep the groove from looking metronomic.
		if beat%4 == 0 || random.unit() > 0.14 {
			events = append(events, spectrumEvent{
				frame: frameAt(float64(beat)), center: barAt(0.055 + 0.08*random.unit()),
				spread: 1.25 + 0.65*random.unit(), amplitude: 0.52 + 0.18*random.unit(),
				attack: 1, decay: 5 + random.integer(3),
			})
		}
		// Alternating snare/clap energy excites separate mid clusters plus a
		// smaller high-frequency reflection.
		if beat%2 == 1 {
			frame := frameAt(float64(beat))
			events = append(events,
				spectrumEvent{frame: frame, center: barAt(0.30 + 0.12*random.unit()), spread: 1.35 + 0.55*random.unit(), amplitude: 0.38 + 0.14*random.unit(), attack: 1, decay: 4 + random.integer(3)},
				spectrumEvent{frame: frame, center: barAt(0.51 + 0.13*random.unit()), spread: 1.25 + 0.55*random.unit(), amplitude: 0.34 + 0.13*random.unit(), attack: 1, decay: 4 + random.integer(2)},
				spectrumEvent{frame: frame, center: barAt(0.74 + 0.12*random.unit()), spread: 0.90 + 0.45*random.unit(), amplitude: 0.20 + 0.09*random.unit(), attack: 1, decay: 3},
			)
		}
		// Half-beat hats keep the right side quick and responsive.
		hatFrame := frameAt(float64(beat) + 0.5)
		events = append(events, spectrumEvent{
			frame: hatFrame, center: barAt(0.68 + 0.26*random.unit()), spread: 0.75 + 0.40*random.unit(),
			amplitude: 0.28 + 0.13*random.unit(), attack: 1, decay: 2 + random.integer(2),
		})
		if random.unit() > 0.62 {
			events = append(events, spectrumEvent{
				frame: frameAt(float64(beat) + 0.75), center: barAt(0.80 + 0.16*random.unit()), spread: 0.65 + 0.35*random.unit(),
				amplitude: 0.24 + 0.12*random.unit(), attack: 1, decay: 2,
			})
		}
	}

	// Seeded melodic accents move compact formants through the low-mid and mid
	// range. These are deliberately sparse so the result stays surprising.
	for range 8 + random.integer(5) {
		events = append(events, spectrumEvent{
			frame: random.integer(VisualizerFrameCount), center: barAt(0.16 + 0.66*random.unit()),
			spread: 0.85 + 1.25*random.unit(), amplitude: 0.42 + 0.28*random.unit(),
			attack: 1 + random.integer(2), decay: 4 + random.integer(5),
		})
	}
	return events
}

func spectrumEventEnvelope(frame int, event spectrumEvent) float64 {
	if frame == event.frame {
		return 1
	}
	before := (event.frame - frame + VisualizerFrameCount) % VisualizerFrameCount
	if before > 0 && before <= event.attack {
		progress := 1 - float64(before)/float64(max(1, event.attack))
		return 0.5 - 0.5*math.Cos(math.Pi*progress)
	}
	after := (frame - event.frame + VisualizerFrameCount) % VisualizerFrameCount
	if after > 0 && after <= event.decay {
		return math.Exp(-2.4 * float64(after) / float64(max(1, event.decay)))
	}
	return 0
}

// followSpectrumTargets finds a periodic starting level for each column, then
// follows the musical targets with a two-pixel attack and one-pixel release.
// Unlike trimming narrow peaks after the fact, this preserves transients while
// guaranteeing a seamless loop and natural meter-like decay.
func followSpectrumTargets(targets [][]int, maximumHeight int) [][]int {
	result := make([][]int, len(targets))
	for frameIndex := range result {
		result[frameIndex] = make([]int, len(targets[frameIndex]))
	}
	if len(targets) == 0 {
		return result
	}
	for x := range targets[0] {
		start := targets[0][x]
		for candidate := 1; candidate <= maximumHeight; candidate++ {
			height := candidate
			for frameIndex := range targets {
				height = approachHeight(height, targets[frameIndex][x])
			}
			if height == candidate {
				start = candidate
				break
			}
		}
		height := start
		for frameIndex := range targets {
			height = approachHeight(height, targets[frameIndex][x])
			result[frameIndex][x] = height
		}
	}
	return result
}

func approachHeight(current, target int) int {
	if target > current {
		return min(target, current+2)
	}
	if target < current {
		return current - 1
	}
	return current
}

type spectrumRandom struct {
	state uint64
}

func newSpectrumRandom(palette [3]color.NRGBA) spectrumRandom {
	seed := uint64(1469598103934665603)
	for _, entry := range palette {
		for _, channel := range [...]uint8{entry.R, entry.G, entry.B} {
			seed ^= uint64(channel)
			seed *= 1099511628211
		}
	}
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return spectrumRandom{state: seed}
}

func (random *spectrumRandom) next() uint64 {
	value := random.state
	value ^= value >> 12
	value ^= value << 25
	value ^= value >> 27
	random.state = value
	return value * 2685821657736338717
}

func (random *spectrumRandom) unit() float64 {
	return float64(random.next()>>11) / float64(uint64(1)<<53)
}

func (random *spectrumRandom) integer(limit int) int {
	if limit <= 1 {
		return 0
	}
	return int(random.next() % uint64(limit))
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

	neutral := colors[0].C < 0.04
	primaryChroma := clamp(colors[0].C*1.18, 0.13, 0.20)
	if neutral {
		primaryChroma = min(0.045, colors[0].C*1.35)
	}
	for index := 1; index < len(colors); index++ {
		if colors[index].C < 0.045 {
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
			lightness := 0.38 + 0.44*math.Pow(position, 0.84)
			chroma := primaryChroma * (1 - 0.15*position)
			hue := normalizeHue(bottomHue + shortestHueDelta(bottomHue, topHue)*position)
			theme.body[x][y] = oklchToColor(oklchColor{L: lightness, C: chroma, H: hue})
		}

		capChroma := clamp(colors[0].C*1.12, 0.13, 0.19)
		if neutral {
			capChroma = min(0.055, colors[0].C*1.35)
		}
		theme.cap[x] = oklchToColor(oklchColor{L: 0.84, C: capChroma, H: colors[0].H})
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

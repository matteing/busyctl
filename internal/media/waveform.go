package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
)

const (
	WaveformFramesPerSecond = 15
	WaveformFrameCount      = 45
)

// WaveformPNGs creates a seamless bank of spectrum-visualizer frames. Every
// horizontal pixel is an independently animated frequency bin, with stronger
// low-end transients and finer, faster high-frequency movement.
func WaveformPNGs(width, height int, dominant string) ([][]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid waveform dimensions %dx%d", width, height)
	}
	palette := waveformPalette(parseColor(dominant))
	frames := make([][]byte, 0, WaveformFrameCount)
	for frame := 0; frame < WaveformFrameCount; frame++ {
		pixels := image.NewPaletted(image.Rect(0, 0, width, height), palette)
		for x, barHeight := range spectrumHeights(width, height, frame) {
			top := height - barHeight
			for y := top; y < height; y++ {
				pixels.SetColorIndex(x, y, uint8(1+gradientIndex(y-top, barHeight)))
			}
		}
		var output bytes.Buffer
		if err := png.Encode(&output, pixels); err != nil {
			return nil, fmt.Errorf("encode waveform frame %d: %w", frame, err)
		}
		frames = append(frames, output.Bytes())
	}
	return frames, nil
}

func spectrumHeights(width, height, frame int) []int {
	heights := make([]int, width)
	t := float64(frame%WaveformFrameCount) / WaveformFrameCount
	// Every envelope is a continuous periodic function. Integer frequencies
	// make the three-second bank join frame 179 back to frame 0 without a reset.
	bassPulse := 0.5 + 0.5*math.Sin(2*math.Pi*t*4-0.7)
	slowSway := 0.5 + 0.5*math.Sin(2*math.Pi*t*2+0.4)
	highPulse := 0.5 + 0.5*math.Sin(2*math.Pi*t*5+1.1)

	for x := range width {
		frequency := float64(x) / float64(max(width-1, 1))
		// Each bin gets stable pseudo-random phases and integer temporal
		// frequencies, which keeps frame 59 -> 0 perfectly seamless.
		phaseA := hashUnit(x*17+11) * 2 * math.Pi
		phaseB := hashUnit(x*31+47) * 2 * math.Pi
		rateA := float64(2 + (x*7)%3)
		rateB := float64(3 + (x*11)%4)
		texture := 0.5 + 0.14*math.Sin(2*math.Pi*t*rateA+phaseA) +
			0.07*math.Sin(2*math.Pi*t*rateB+phaseB)
		texture = min(max(texture, 0.18), 0.82)

		lowEnd := math.Pow(1-frequency, 1.7)
		midPunch := math.Exp(-math.Pow((frequency-0.42)/0.22, 2))
		highFizz := math.Pow(frequency, 0.65)
		energy := 0.10 + texture*(0.38+0.14*midPunch) +
			bassPulse*(0.16*lowEnd+0.04*midPunch) +
			slowSway*(0.05*lowEnd+0.04*midPunch) +
			highPulse*(0.03*midPunch+0.07*highFizz)
		// A small neighboring-bin term creates spectral clusters without
		// turning the bins into a traveling sine wave.
		neighbor := 0.5 + 0.5*math.Sin(2*math.Pi*t*float64(2+(x/5)%4)+hashUnit((x/3)*23)*2*math.Pi)
		energy = 0.88*energy + 0.12*neighbor
		heights[x] = 1 + int(math.Round(min(energy, 1)*float64(height-1)))
	}
	return heights
}

func hashUnit(value int) float64 {
	number := uint32(value)*1664525 + 1013904223
	number ^= number >> 16
	return float64(number&0xffff) / 65535
}

func waveformPalette(base color.NRGBA) color.Palette {
	palette := make(color.Palette, 33)
	palette[0] = color.NRGBA{A: 255}
	for index := 0; index < 16; index++ {
		position := float64(index) / 15
		var tone color.NRGBA
		if position < 0.5 {
			tone = blend(base, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, (0.5-position)*0.9)
		} else {
			tone = blend(base, color.NRGBA{A: 255}, (position-0.5)*0.7)
		}
		palette[1+index] = tone
		palette[17+index] = blend(tone, color.NRGBA{A: 255}, 0.65)
	}
	return palette
}

func gradientIndex(y, height int) int {
	if height <= 1 {
		return 0
	}
	return min(max(y*15/(height-1), 0), 15)
}

func blend(from, to color.NRGBA, amount float64) color.NRGBA {
	channel := func(a, b uint8) uint8 {
		return uint8(math.Round(float64(a)*(1-amount) + float64(b)*amount))
	}
	return color.NRGBA{
		R: channel(from.R, to.R), G: channel(from.G, to.G),
		B: channel(from.B, to.B), A: 255,
	}
}

func parseColor(value string) color.NRGBA {
	fallback := color.NRGBA{R: 250, G: 45, B: 72, A: 255}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "rgb(") && strings.HasSuffix(value, ")") {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "rgb("), ")"), ",")
		if len(parts) == 3 {
			channels := [3]uint8{}
			for index, part := range parts {
				number, err := strconv.Atoi(strings.TrimSpace(part))
				if err != nil || number < 0 || number > 255 {
					return fallback
				}
				channels[index] = uint8(number)
			}
			return color.NRGBA{R: channels[0], G: channels[1], B: channels[2], A: 255}
		}
	}
	if len(value) == 7 && value[0] == '#' {
		number, err := strconv.ParseUint(value[1:], 16, 24)
		if err == nil {
			return color.NRGBA{R: uint8(number >> 16), G: uint8(number >> 8), B: uint8(number), A: 255}
		}
	}
	return fallback
}

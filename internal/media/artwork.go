package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

const (
	maxArtworkBytes   = 8 << 20
	maxArtworkPixels  = 25_000_000
	paletteSampleSize = 48
)

// Artwork contains the two versions of an album cover used by the display and
// three representative colors for theming the visualizer.
type Artwork struct {
	ColorPNG     []byte
	GrayscalePNG []byte
	Palette      [3]color.NRGBA
}

var artworkClient = &http.Client{Timeout: 20 * time.Second}

// PrepareArtwork downloads an image, center-crops it to a square, scales it to
// size, and returns both color and grayscale PNGs. Palette extraction uses a
// slightly larger sample than the display image so small covers retain their
// characteristic colors.
func PrepareArtwork(rawURL string, size int) (Artwork, error) {
	if size <= 0 {
		return Artwork{}, fmt.Errorf("invalid artwork size %d", size)
	}

	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return Artwork{}, fmt.Errorf("create artwork request: %w", err)
	}
	response, err := artworkClient.Do(request)
	if err != nil {
		return Artwork{}, fmt.Errorf("download artwork: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Artwork{}, fmt.Errorf("download artwork: server returned %s", response.Status)
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxArtworkBytes+1))
	if err != nil {
		return Artwork{}, fmt.Errorf("read artwork: %w", err)
	}
	if len(payload) > maxArtworkBytes {
		return Artwork{}, fmt.Errorf("artwork exceeds %d bytes", maxArtworkBytes)
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return Artwork{}, fmt.Errorf("decode artwork metadata: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxArtworkPixels {
		return Artwork{}, fmt.Errorf("invalid artwork dimensions %dx%d", config.Width, config.Height)
	}

	source, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return Artwork{}, fmt.Errorf("decode artwork: %w", err)
	}

	colorImage := cropAndScale(source, size)
	applySquircleMask(colorImage)
	colorPNG, err := encodePNG(colorImage)
	if err != nil {
		return Artwork{}, fmt.Errorf("encode artwork PNG: %w", err)
	}
	grayscalePNG, err := GrayscalePNG(colorPNG)
	if err != nil {
		return Artwork{}, fmt.Errorf("make grayscale artwork: %w", err)
	}

	return Artwork{
		ColorPNG:     colorPNG,
		GrayscalePNG: grayscalePNG,
		Palette:      extractPalette(cropAndScale(source, paletteSampleSize)),
	}, nil
}

// GrayscalePNG converts a generated PNG to luminance while retaining its size
// and alpha channel. It deliberately accepts PNG only so callers cannot
// accidentally put an expensive network image decode in the render loop.
func GrayscalePNG(payload []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("decode PNG: %w", err)
	}
	bounds := source.Bounds()
	destination := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			// Integer Rec. 709 luminance coefficients sum to 256.
			luminance := uint8((54*uint16(pixel.R) + 183*uint16(pixel.G) + 19*uint16(pixel.B) + 128) >> 8)
			destination.SetNRGBA(x-bounds.Min.X, y-bounds.Min.Y, color.NRGBA{
				R: luminance,
				G: luminance,
				B: luminance,
				A: pixel.A,
			})
		}
	}

	encoded, err := encodePNG(destination)
	if err != nil {
		return nil, fmt.Errorf("encode grayscale PNG: %w", err)
	}
	return encoded, nil
}

func cropAndScale(source image.Image, size int) *image.NRGBA {
	bounds := source.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	left := bounds.Min.X + (bounds.Dx()-side)/2
	top := bounds.Min.Y + (bounds.Dy()-side)/2

	destination := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			// Sample the center of each destination pixel. Nearest-neighbor is
			// intentional: at BUSY Bar scale it preserves crisp album details.
			sourceX := left + (2*x+1)*side/(2*size)
			sourceY := top + (2*y+1)*side/(2*size)
			if sourceX >= left+side {
				sourceX = left + side - 1
			}
			if sourceY >= top+side {
				sourceY = top + side - 1
			}
			destination.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(sourceX, sourceY)).(color.NRGBA))
		}
	}
	return destination
}

// applySquircleMask rounds the tiny album cover with a fifth-order
// superellipse. Supersampling gives the handful of boundary pixels partial
// alpha, which looks smoother on the physical LEDs than a staircase cutout.
func applySquircleMask(destination *image.NRGBA) {
	if destination == nil || destination.Bounds().Empty() {
		return
	}
	bounds := destination.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			coverage := squircleCoverage(x-bounds.Min.X, y-bounds.Min.Y, bounds.Dx(), bounds.Dy())
			if coverage >= 1 {
				continue
			}
			pixel := destination.NRGBAAt(x, y)
			pixel.A = uint8(math.Round(float64(pixel.A) * coverage))
			destination.SetNRGBA(x, y, pixel)
		}
	}
}

func squircleCoverage(x, y, width, height int) float64 {
	if width <= 0 || height <= 0 {
		return 0
	}
	const samples = 4
	centerX, centerY := float64(width)/2, float64(height)/2
	radiusX, radiusY := float64(width)/2, float64(height)/2
	inside := 0
	for sampleY := range samples {
		for sampleX := range samples {
			pointX := float64(x) + (float64(sampleX)+0.5)/samples
			pointY := float64(y) + (float64(sampleY)+0.5)/samples
			normalizedX := math.Abs((pointX - centerX) / radiusX)
			normalizedY := math.Abs((pointY - centerY) / radiusY)
			if math.Pow(normalizedX, 5)+math.Pow(normalizedY, 5) <= 1 {
				inside++
			}
		}
	}
	return float64(inside) / (samples * samples)
}

func encodePNG(source image.Image) ([]byte, error) {
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, source); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type paletteBucket struct {
	key        uint16
	count      int
	red        uint64
	green      uint64
	blue       uint64
	color      color.NRGBA
	perceptual oklchColor
	dominance  float64
}

func extractPalette(source image.Image) [3]color.NRGBA {
	buckets := make(map[uint16]*paletteBucket)
	bounds := source.Bounds()
	total := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			if pixel.A < 128 {
				continue
			}
			key := uint16(pixel.R>>4)<<8 | uint16(pixel.G>>4)<<4 | uint16(pixel.B>>4)
			bucket := buckets[key]
			if bucket == nil {
				bucket = &paletteBucket{key: key}
				buckets[key] = bucket
			}
			bucket.count++
			bucket.red += uint64(pixel.R)
			bucket.green += uint64(pixel.G)
			bucket.blue += uint64(pixel.B)
			total++
		}
	}

	minimumCount := max(2, total/500)
	maximumCount := 0
	for _, bucket := range buckets {
		maximumCount = max(maximumCount, bucket.count)
	}
	candidates := make([]paletteBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if bucket.count < minimumCount {
			continue
		}
		count := uint64(bucket.count)
		bucket.color = color.NRGBA{
			R: uint8(bucket.red / count),
			G: uint8(bucket.green / count),
			B: uint8(bucket.blue / count),
			A: 255,
		}
		bucket.perceptual = colorToOKLCH(bucket.color)
		frequency := math.Sqrt(float64(bucket.count) / float64(max(1, maximumCount)))
		chroma := smoothstep(0.025, 0.16, bucket.perceptual.C)
		readability := math.Exp(-math.Pow((bucket.perceptual.L-0.62)/0.28, 2))
		if bucket.perceptual.L < 0.16 {
			readability *= bucket.perceptual.L / 0.16
		} else if bucket.perceptual.L > 0.94 {
			readability *= (1 - bucket.perceptual.L) / 0.06
		}
		// Frequency keeps the theme representative, while chroma and readable
		// lightness prevent black backgrounds, beige fields, and tiny white
		// highlights from automatically becoming the primary visualizer color.
		bucket.dominance = 0.35*frequency + 0.45*chroma + 0.20*max(0, readability)
		candidates = append(candidates, *bucket)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dominance == candidates[j].dominance {
			return candidates[i].key < candidates[j].key
		}
		return candidates[i].dominance > candidates[j].dominance
	})

	if len(candidates) == 0 {
		return [3]color.NRGBA{
			{R: 96, G: 96, B: 104, A: 255},
			{R: 152, G: 152, B: 160, A: 255},
			{R: 220, G: 220, B: 224, A: 255},
		}
	}

	hasChromaticColor := false
	for _, candidate := range candidates {
		if candidate.perceptual.C >= 0.04 {
			hasChromaticColor = true
			break
		}
	}
	primaryIndex := 0
	if hasChromaticColor {
		for index, candidate := range candidates {
			if candidate.perceptual.C >= 0.025 {
				primaryIndex = index
				break
			}
		}
	}
	selected := []paletteBucket{candidates[primaryIndex]}
	used := map[uint16]bool{candidates[primaryIndex].key: true}
	for len(selected) < 3 && len(used) < len(candidates) {
		bestIndex := -1
		bestScore := -1.0
		for index, candidate := range candidates {
			if used[candidate.key] {
				continue
			}
			if hasChromaticColor && candidate.perceptual.C < 0.025 {
				continue
			}
			minimumDistance := 1.0
			for _, existing := range selected {
				distance := min(1, oklabDistance(candidate.perceptual, existing.perceptual)/0.35)
				if distance < minimumDistance {
					minimumDistance = distance
				}
			}
			score := candidate.dominance * (0.35 + 0.65*minimumDistance)
			if score > bestScore {
				bestIndex = index
				bestScore = score
			}
		}
		if bestIndex < 0 {
			break
		}
		selected = append(selected, candidates[bestIndex])
		used[candidates[bestIndex].key] = true
	}

	for len(selected) < 3 {
		selected = append(selected, selected[0])
	}

	return [3]color.NRGBA{selected[0].color, selected[1].color, selected[2].color}
}

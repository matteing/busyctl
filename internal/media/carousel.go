package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

// CarouselPNGs composes artwork into a cyclic horizontal strip and creates a
// bank of horizontally interpolated subpixel frames. Each frame is a complete
// strip, allowing the display to move the carousel as one atomic image layer.
func CarouselPNGs(artwork [][]byte, gap, minWidth, phases int) ([][]byte, int, error) {
	if len(artwork) == 0 || gap < 0 || minWidth <= 0 || phases <= 0 {
		return nil, 0, fmt.Errorf("invalid carousel configuration")
	}
	decoded := make([]image.Image, 0, len(artwork))
	for index, payload := range artwork {
		cover, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			return nil, 0, fmt.Errorf("decode carousel cover %d: %w", index+1, err)
		}
		decoded = append(decoded, cover)
	}

	size := decoded[0].Bounds().Dx()
	if size <= 0 || decoded[0].Bounds().Dy() != size {
		return nil, 0, fmt.Errorf("carousel covers must be square")
	}
	stride := size + gap
	itemCount := max(len(decoded), int(math.Ceil(float64(minWidth)/float64(stride))))
	stripWidth := itemCount * stride
	strip := image.NewNRGBA(image.Rect(0, 0, stripWidth, size))
	draw.Draw(strip, strip.Bounds(), &image.Uniform{C: color.NRGBA{A: 255}}, image.Point{}, draw.Src)
	for index := range itemCount {
		cover := decoded[index%len(decoded)]
		destination := image.Rect(index*stride, 0, index*stride+size, size)
		draw.Draw(strip, destination, cover, cover.Bounds().Min, draw.Src)
	}

	frames := make([][]byte, 0, phases)
	for phase := range phases {
		amount := float64(phase) / float64(phases)
		frame := image.NewNRGBA(strip.Bounds())
		for y := range size {
			for x := range stripWidth {
				left := color.NRGBAModel.Convert(strip.At(x, y)).(color.NRGBA)
				right := color.NRGBAModel.Convert(strip.At((x+1)%stripWidth, y)).(color.NRGBA)
				frame.SetNRGBA(x, y, blendNRGBA(left, right, amount))
			}
		}
		var output bytes.Buffer
		if err := png.Encode(&output, frame); err != nil {
			return nil, 0, fmt.Errorf("encode carousel phase %d: %w", phase, err)
		}
		frames = append(frames, output.Bytes())
	}
	return frames, stripWidth, nil
}

// TilePNGs splits every carousel frame into display-sized tiles. Interpolation
// is already baked into the complete frame, so pixels remain continuous at
// tile boundaries.
func TilePNGs(frames [][]byte, maxWidth int) ([][][]byte, []int, error) {
	if len(frames) == 0 || maxWidth <= 0 {
		return nil, nil, fmt.Errorf("carousel frames and a positive tile width are required")
	}

	var tiled [][][]byte
	var widths []int
	for frameIndex, payload := range frames {
		frame, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			return nil, nil, fmt.Errorf("decode carousel frame %d: %w", frameIndex+1, err)
		}
		bounds := frame.Bounds()
		if frameIndex == 0 {
			for x := 0; x < bounds.Dx(); x += maxWidth {
				widths = append(widths, min(maxWidth, bounds.Dx()-x))
			}
		}

		frameTiles := make([][]byte, 0, len(widths))
		x := 0
		for tileIndex, width := range widths {
			tile := image.NewNRGBA(image.Rect(0, 0, width, bounds.Dy()))
			draw.Draw(tile, tile.Bounds(), frame, image.Pt(bounds.Min.X+x, bounds.Min.Y), draw.Src)
			var output bytes.Buffer
			if err := png.Encode(&output, tile); err != nil {
				return nil, nil, fmt.Errorf("encode carousel frame %d tile %d: %w", frameIndex+1, tileIndex+1, err)
			}
			frameTiles = append(frameTiles, output.Bytes())
			x += width
		}
		tiled = append(tiled, frameTiles)
	}
	return tiled, widths, nil
}

func blendNRGBA(left, right color.NRGBA, amount float64) color.NRGBA {
	channel := func(a, b uint8) uint8 {
		return uint8(math.Round(float64(a)*(1-amount) + float64(b)*amount))
	}
	return color.NRGBA{
		R: channel(left.R, right.R), G: channel(left.G, right.G),
		B: channel(left.B, right.B), A: channel(left.A, right.A),
	}
}

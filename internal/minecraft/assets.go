package minecraft

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

type displayAsset struct {
	path    string
	payload []byte
}

func minecraftAssets() []displayAsset {
	assets := make([]displayAsset, 0, logoFrameCount+4)
	for frame := 0; frame < logoFrameCount; frame++ {
		assets = append(assets, displayAsset{path: logoPath(frame), payload: creeperLogoPNG(frame)})
	}
	return append(assets,
		displayAsset{path: onlinePath, payload: statusPNG(true)},
		displayAsset{path: offlinePath, payload: statusPNG(false)},
		displayAsset{path: checkingPath, payload: checkingPNG()},
		displayAsset{path: playersPath, payload: playersPNG()},
	)
}

// creeperLogoPNG builds one frame of a slow, seamless diagonal shimmer. The
// face remains fixed while the green gradient and two tiny glints ease through
// a roughly five-second loop.
func creeperLogoPNG(frame int) []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, logoSize, logoSize))
	phase := float64(frame%logoFrameCount) / float64(logoFrameCount)
	topLeft := color.NRGBA{R: 0x78, G: 0xe8, B: 0x5b, A: 0xff}
	bottomRight := color.NRGBA{R: 0x13, G: 0x63, B: 0x43, A: 0xff}
	shimmer := color.NRGBA{R: 0xc8, G: 0xff, B: 0xa8, A: 0xff}
	texture := [...]int{-10, 8, 0, 13, -6, 5}
	for y := 0; y < logoSize; y++ {
		for x := 0; x < logoSize; x++ {
			gradient := float64(x+y) / float64(2*(logoSize-1))
			base := mixColor(topLeft, bottomRight, gradient)
			base = shiftColor(base, texture[(x/2+3*(y/2)+(x*y)%2)%len(texture)])
			wave := 0.5 + 0.5*math.Cos(2*math.Pi*(phase-gradient))
			light := 0.22 * math.Pow(wave, 7)
			canvas.SetNRGBA(x, y, mixColor(base, shimmer, light))
		}
	}
	// Transparent corner pixels keep the block visually aligned with the
	// rounded album-art treatment used by the music applet.
	transparent := color.NRGBA{}
	for _, point := range [][2]int{{0, 0}, {logoSize - 1, 0}, {0, logoSize - 1}, {logoSize - 1, logoSize - 1}} {
		canvas.SetNRGBA(point[0], point[1], transparent)
	}
	face := color.NRGBA{R: 0x10, G: 0x2f, B: 0x20, A: 0xff}
	fillRect(canvas, 2, 3, 3, 3, face)
	fillRect(canvas, 9, 3, 3, 3, face)
	fillRect(canvas, 6, 6, 2, 3, face)
	fillRect(canvas, 4, 8, 6, 3, face)
	fillRect(canvas, 4, 11, 2, 2, face)
	fillRect(canvas, 8, 11, 2, 2, face)
	paintSparkle(canvas, 6, 1, phase, 0)
	paintSparkle(canvas, 12, 8, phase, 0.43)
	return encodePNG(canvas)
}

func statusPNG(online bool) []byte {
	if online {
		return onlineButtonPNG()
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	fill := color.NRGBA{R: 0xff, G: 0x57, B: 0x52, A: 0xff}
	paintDisc(canvas, fill)
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	points := [][2]int{{1, 1}, {3, 1}, {2, 2}, {1, 3}, {3, 3}}
	for _, point := range points {
		canvas.SetNRGBA(point[0], point[1], white)
	}
	return encodePNG(canvas)
}

// onlineButtonPNG is a tiny glossy status lamp: a mint specular highlight in
// the upper-left eases through saturated green into a deep lower-right rim.
// The lighting supplies the online affordance without drawing a checkmark.
func onlineButtonPNG() []byte {
	transparent := color.NRGBA{}
	pixels := [iconSize][iconSize]color.NRGBA{
		{transparent, rgba(0x70, 0xff, 0x8c), rgba(0x55, 0xef, 0x73), rgba(0x2b, 0xb9, 0x50), transparent},
		{rgba(0x78, 0xff, 0x94), rgba(0xd9, 0xff, 0xd8), rgba(0x64, 0xf8, 0x7b), rgba(0x2e, 0xc9, 0x56), rgba(0x12, 0x7f, 0x38)},
		{rgba(0x58, 0xeb, 0x72), rgba(0x62, 0xf6, 0x79), rgba(0x3e, 0xda, 0x63), rgba(0x1f, 0xad, 0x49), rgba(0x0b, 0x67, 0x2f)},
		{rgba(0x2e, 0xc3, 0x54), rgba(0x2a, 0xbf, 0x50), rgba(0x19, 0x9f, 0x42), rgba(0x0b, 0x78, 0x34), rgba(0x06, 0x4c, 0x25)},
		{transparent, rgba(0x12, 0x7e, 0x37), rgba(0x0a, 0x68, 0x2f), rgba(0x05, 0x4b, 0x25), transparent},
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y, row := range pixels {
		for x, pixel := range row {
			canvas.SetNRGBA(x, y, pixel)
		}
	}
	return encodePNG(canvas)
}

func checkingPNG() []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	paintDisc(canvas, color.NRGBA{R: 0xff, G: 0xc9, B: 0x4d, A: 0xff})
	dark := color.NRGBA{R: 0x55, G: 0x3e, B: 0x10, A: 0xff}
	for _, point := range [][2]int{{2, 1}, {2, 2}, {2, 3}} {
		canvas.SetNRGBA(point[0], point[1], dark)
	}
	return encodePNG(canvas)
}

func playersPNG() []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	light := color.NRGBA{R: 0xd8, G: 0xd8, B: 0xd8, A: 0xff}
	shade := color.NRGBA{R: 0x83, G: 0x8a, B: 0x93, A: 0xff}
	fillRect(canvas, 1, 0, 3, 3, light)
	canvas.SetNRGBA(1, 1, shade)
	canvas.SetNRGBA(3, 1, shade)
	fillRect(canvas, 1, 3, 3, 1, light)
	fillRect(canvas, 0, 4, 5, 1, shade)
	return encodePNG(canvas)
}

func paintDisc(canvas *image.NRGBA, fill color.NRGBA) {
	for y, row := range []string{"01110", "11111", "11111", "11111", "01110"} {
		for x, pixel := range row {
			if pixel == '1' {
				canvas.SetNRGBA(x, y, fill)
			}
		}
	}
}

func fillRect(canvas *image.NRGBA, x, y, width, height int, fill color.NRGBA) {
	for currentY := y; currentY < y+height; currentY++ {
		for currentX := x; currentX < x+width; currentX++ {
			canvas.SetNRGBA(currentX, currentY, fill)
		}
	}
}

func paintSparkle(canvas *image.NRGBA, x, y int, phase, offset float64) {
	pulse := 0.5 + 0.5*math.Cos(2*math.Pi*(phase+offset))
	pulse = pulse * pulse * (3 - 2*pulse)
	center := color.NRGBA{R: 0xee, G: 0xff, B: 0xd9, A: 0xff}
	halo := color.NRGBA{R: 0xa8, G: 0xff, B: 0x83, A: 0xff}
	lightenPixel(canvas, x, y, center, 0.22+0.72*pulse)
	for _, point := range [][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
		if point[0] >= 0 && point[0] < logoSize && point[1] >= 0 && point[1] < logoSize {
			lightenPixel(canvas, point[0], point[1], halo, 0.04+0.28*pulse)
		}
	}
}

func lightenPixel(canvas *image.NRGBA, x, y int, highlight color.NRGBA, amount float64) {
	canvas.SetNRGBA(x, y, mixColor(canvas.NRGBAAt(x, y), highlight, amount))
}

func mixColor(from, to color.NRGBA, amount float64) color.NRGBA {
	if amount < 0 {
		amount = 0
	} else if amount > 1 {
		amount = 1
	}
	mix := func(start, end uint8) uint8 {
		return uint8(math.Round(float64(start) + (float64(end)-float64(start))*amount))
	}
	return color.NRGBA{R: mix(from.R, to.R), G: mix(from.G, to.G), B: mix(from.B, to.B), A: 0xff}
}

func shiftColor(source color.NRGBA, amount int) color.NRGBA {
	shift := func(value uint8) uint8 {
		adjusted := int(value) + amount
		if adjusted < 0 {
			return 0
		}
		if adjusted > 255 {
			return 255
		}
		return uint8(adjusted)
	}
	return color.NRGBA{R: shift(source.R), G: shift(source.G), B: shift(source.B), A: source.A}
}

func rgba(red, green, blue uint8) color.NRGBA {
	return color.NRGBA{R: red, G: green, B: blue, A: 0xff}
}

func logoPath(frame int) string {
	return fmt.Sprintf("minecraft-%02d.png", frame%logoFrameCount)
}

func encodePNG(canvas image.Image) []byte {
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		// Encoding an in-memory NRGBA cannot currently fail. Panic here keeps the
		// asset helper honest if the standard encoder contract ever changes.
		panic(fmt.Sprintf("encode Minecraft display asset: %v", err))
	}
	return output.Bytes()
}

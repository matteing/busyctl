package media

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"
)

const maxArtworkBytes = 8 << 20

var artworkClient = &http.Client{Timeout: 20 * time.Second}

func DownloadSquarePNG(rawURL string, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid artwork size %d", size)
	}
	resp, err := artworkClient.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("download artwork: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download artwork: server returned %s", resp.Status)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read artwork: %w", err)
	}
	if len(payload) > maxArtworkBytes {
		return nil, fmt.Errorf("artwork exceeds %d bytes", maxArtworkBytes)
	}
	source, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("decode artwork: %w", err)
	}

	destination := cropAndScale(source, size)
	var output bytes.Buffer
	if err := png.Encode(&output, destination); err != nil {
		return nil, fmt.Errorf("encode artwork PNG: %w", err)
	}
	return output.Bytes(), nil
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
			sourceX := left + (2*x+1)*side/(2*size)
			sourceY := top + (2*y+1)*side/(2*size)
			destination.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return destination
}

// Register the artwork formats supported by image.Decode.
var (
	_ = gif.GIF{}
	_ = jpeg.DefaultQuality
)

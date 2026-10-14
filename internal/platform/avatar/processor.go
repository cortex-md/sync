package avatar

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxSourcePixels = 40_000_000
	jpegQuality     = 85
)

type Processor struct {
	maxBytes int64
	size     int
}

func NewProcessor(maxBytes int64, size int) *Processor {
	return &Processor{maxBytes: maxBytes, size: size}
}

func (p *Processor) Process(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, p.maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > p.maxBytes {
		return nil, domain.ErrFileTooLarge
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported image", domain.ErrInvalidInput)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxSourcePixels {
		return nil, fmt.Errorf("%w: image dimensions out of range", domain.ErrInvalidInput)
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported image", domain.ErrInvalidInput)
	}

	bounds := src.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(
		bounds.Min.X+(bounds.Dx()-side)/2,
		bounds.Min.Y+(bounds.Dy()-side)/2,
	))

	dst := image.NewRGBA(image.Rect(0, 0, p.size, p.size))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

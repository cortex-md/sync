package avatar_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/avatar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessorCropsAndResizesToSquareJPEG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 900, 300))
	for x := 0; x < 900; x++ {
		for y := 0; y < 300; y++ {
			src.Set(x, y, color.RGBA{R: uint8(x % 255), A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, src))

	out, err := avatar.NewProcessor(5<<20, 256).Process(&buf)
	require.NoError(t, err)

	decoded, err := jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 256, 256), decoded.Bounds())
}

func TestProcessorRejectsNonImagesAndOversizedInput(t *testing.T) {
	_, err := avatar.NewProcessor(5<<20, 256).Process(strings.NewReader("not an image"))
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	_, err = avatar.NewProcessor(10, 256).Process(bytes.NewReader(make([]byte, 64)))
	assert.ErrorIs(t, err, domain.ErrFileTooLarge)
}

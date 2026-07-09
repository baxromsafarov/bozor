package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePNG строит тестовое PNG-изображение w×h.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestDecode_Dimensions(t *testing.T) {
	dec, err := Decode(makePNG(t, 200, 100))
	require.NoError(t, err)
	assert.Equal(t, 200, dec.Width)
	assert.Equal(t, 100, dec.Height)
}

func TestDecode_Invalid(t *testing.T) {
	_, err := Decode([]byte("not an image"))
	assert.Error(t, err)
}

func TestFit_ScalesDownPreservingAspect(t *testing.T) {
	dec, err := Decode(makePNG(t, 200, 100))
	require.NoError(t, err)
	fit := Fit(dec.Img, 120)
	w, h := Dimensions(fit)
	assert.Equal(t, 120, w, "длинная сторона приведена к 120")
	assert.Equal(t, 60, h, "пропорция 2:1 сохранена")
}

func TestFit_DoesNotUpscale(t *testing.T) {
	dec, err := Decode(makePNG(t, 200, 100))
	require.NoError(t, err)
	fit := Fit(dec.Img, 1080) // цель больше оригинала
	w, h := Dimensions(fit)
	assert.Equal(t, 200, w, "оригинал меньше цели — без увеличения")
	assert.Equal(t, 100, h)
}

func TestEncode_JPEGAndPNG(t *testing.T) {
	dec, err := Decode(makePNG(t, 64, 64))
	require.NoError(t, err)

	j, err := Encode(dec.Img, false)
	require.NoError(t, err)
	_, format, err := image.Decode(bytes.NewReader(j))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)

	p, err := Encode(dec.Img, true)
	require.NoError(t, err)
	_, format, err = image.Decode(bytes.NewReader(p))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
}

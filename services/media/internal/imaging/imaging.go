// Package imaging инкапсулирует обработку изображений Media-сервиса:
// декодирование с учётом EXIF-ориентации, масштабирование превью (вписыванием,
// без увеличения) и кодирование. Повторное кодирование удаляет EXIF-метаданные
// (в т.ч. GPS) — исходные данные не переносятся в результат.
//
// Реализация чисто на Go (без cgo): github.com/disintegration/imaging поверх
// стандартных image/jpeg, image/png и golang.org/x/image/webp (декодер WebP).
package imaging

import (
	"bytes"
	"fmt"
	"image"

	"github.com/disintegration/imaging"

	_ "golang.org/x/image/webp" // регистрация декодера WebP для image.Decode
)

// jpegQuality — качество JPEG-кодирования превью.
const jpegQuality = 82

// Decoded — декодированное изображение с применённой EXIF-ориентацией.
type Decoded struct {
	Img    image.Image
	Width  int
	Height int
}

// Decode декодирует изображение из data и разворачивает его по EXIF-ориентации
// (Orientation), после чего EXIF больше не нужен и при кодировании отбрасывается.
func Decode(data []byte) (Decoded, error) {
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return Decoded{}, fmt.Errorf("imaging: декодирование: %w", err)
	}
	b := img.Bounds()
	return Decoded{Img: img, Width: b.Dx(), Height: b.Dy()}, nil
}

// Fit вписывает изображение в квадрат size×size с сохранением пропорций и БЕЗ
// увеличения: если оригинал меньше size, возвращается копия исходного размера.
func Fit(img image.Image, size int) image.Image {
	return imaging.Fit(img, size, size, imaging.Lanczos)
}

// Encode кодирует изображение: PNG (с сохранением прозрачности) при asPNG,
// иначе JPEG. Результат не содержит EXIF.
func Encode(img image.Image, asPNG bool) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if asPNG {
		err = imaging.Encode(&buf, img, imaging.PNG)
	} else {
		err = imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(jpegQuality))
	}
	if err != nil {
		return nil, fmt.Errorf("imaging: кодирование: %w", err)
	}
	return buf.Bytes(), nil
}

// Dimensions возвращает ширину и высоту изображения.
func Dimensions(img image.Image) (width, height int) {
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

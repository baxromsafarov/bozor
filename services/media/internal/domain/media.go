// Package domain содержит доменные сущности и правила Media-сервиса.
package domain

import (
	"errors"
	"strconv"
	"time"
)

// Доменные ошибки медиа.
var (
	ErrMediaNotFound   = errors.New("медиа не найдено")
	ErrEmptyFile       = errors.New("пустой файл")
	ErrFileTooLarge    = errors.New("файл превышает допустимый размер")
	ErrUnsupportedType = errors.New("неподдерживаемый тип файла")
	ErrAdMediaLimit    = errors.New("превышен лимит медиа на объявление")
	ErrMissingOwner    = errors.New("не указан владелец")
)

// Status — статус жизненного цикла медиа.
type Status string

// Статусы медиа.
const (
	StatusUploaded Status = "uploaded" // оригинал загружен, превью ещё нет
	StatusReady    Status = "ready"    // превью готовы (воркер 3.2)
	StatusOrphan   Status = "orphan"   // не привязано к объявлению, под очистку
)

// Media — загруженный медиафайл (оригинал в объектном хранилище).
type Media struct {
	ID          string
	OwnerUserID string
	AdID        *string // необязательная привязка к объявлению-черновику
	Bucket      string
	ObjectKey   string
	MimeType    string
	SizeBytes   int64
	Status      Status
	Width       *int
	Height      *int
	Previews    []Preview // сгенерированные превью (воркер 3.2); пусто до обработки
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

// Preview — сгенерированное превью оригинала (без EXIF).
type Preview struct {
	Size      int    `json:"size"` // целевой размер длинной стороны (120/480/1080)
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ObjectKey string `json:"object_key"`
}

// PreviewSizes — целевые размеры длинной стороны превью (без увеличения оригинала).
var PreviewSizes = []int{120, 480, 1080}

// AllowedTypes — поддерживаемые MIME-типы изображений и расширение объекта.
var AllowedTypes = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
}

// Limits — ограничения загрузки медиа.
type Limits struct {
	MaxSizeBytes int64 // максимальный размер одного файла
	MaxPerAd     int   // максимум медиа на одно объявление
}

// ExtFor возвращает расширение объекта для MIME-типа и признак поддержки.
func ExtFor(mimeType string) (string, bool) {
	ext, ok := AllowedTypes[mimeType]
	return ext, ok
}

// IsPNGSource сообщает, кодировать ли превью как PNG (сохранение альфа-канала);
// для остальных типов превью кодируются в JPEG.
func IsPNGSource(mimeType string) bool { return mimeType == "image/png" }

// PreviewExt возвращает расширение файла превью для MIME-типа оригинала.
func PreviewExt(mimeType string) string {
	if IsPNGSource(mimeType) {
		return "png"
	}
	return "jpg"
}

// PreviewKey строит object_key превью размера size для медиа id.
func PreviewKey(id string, size int, ext string) string {
	return "previews/" + id + "/" + strconv.Itoa(size) + "." + ext
}

// ValidateUpload проверяет тип и размер загружаемого файла по лимитам.
func ValidateUpload(mimeType string, size int64, lim Limits) error {
	if size <= 0 {
		return ErrEmptyFile
	}
	if _, ok := AllowedTypes[mimeType]; !ok {
		return ErrUnsupportedType
	}
	if size > lim.MaxSizeBytes {
		return ErrFileTooLarge
	}
	return nil
}

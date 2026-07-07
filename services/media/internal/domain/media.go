// Package domain содержит доменные сущности и правила Media-сервиса.
package domain

import (
	"errors"
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
	CreatedAt   time.Time
}

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

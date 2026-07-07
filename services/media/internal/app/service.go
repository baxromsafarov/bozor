// Package app содержит use-cases Media-сервиса.
package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/media/internal/domain"
)

const source = "media"

// Store — персистентность медиа (реализуется repo.Repo).
type Store interface {
	CountByAd(ctx context.Context, adID string) (int, error)
	InsertWithEvent(ctx context.Context, m domain.Media, ev events.Envelope) error
	GetByID(ctx context.Context, id string) (domain.Media, error)
}

// Blob — объектное хранилище оригиналов (реализуется storage.Store).
type Blob interface {
	Bucket() string
	Put(ctx context.Context, objectKey string, r io.Reader, size int64, contentType string) error
	Remove(ctx context.Context, objectKey string) error
	PublicURL(objectKey string) string
}

// Service — use-cases медиа.
type Service struct {
	store  Store
	blob   Blob
	limits domain.Limits
	log    *slog.Logger
}

// NewService создаёт сервис медиа.
func NewService(store Store, blob Blob, limits domain.Limits, log *slog.Logger) *Service {
	return &Service{store: store, blob: blob, limits: limits, log: log}
}

// UploadInput — входные данные загрузки медиа.
type UploadInput struct {
	OwnerUserID string
	AdID        *string
	MimeType    string // определён сервером по содержимому
	Data        []byte
}

// Uploaded — результат загрузки (медиа + публичный URL оригинала).
type Uploaded struct {
	Media     domain.Media
	PublicURL string
}

// Upload проверяет и сохраняет оригинал в хранилище, пишет запись + событие.
func (s *Service) Upload(ctx context.Context, in UploadInput) (Uploaded, error) {
	if in.OwnerUserID == "" {
		return Uploaded{}, domain.ErrMissingOwner
	}
	size := int64(len(in.Data))
	if err := domain.ValidateUpload(in.MimeType, size, s.limits); err != nil {
		return Uploaded{}, err
	}
	if in.AdID != nil {
		n, err := s.store.CountByAd(ctx, *in.AdID)
		if err != nil {
			return Uploaded{}, err
		}
		if n >= s.limits.MaxPerAd {
			return Uploaded{}, domain.ErrAdMediaLimit
		}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Uploaded{}, fmt.Errorf("app: генерация id: %w", err)
	}
	ext, _ := domain.ExtFor(in.MimeType)
	objectKey := "originals/" + id.String() + "." + ext

	if err := s.blob.Put(ctx, objectKey, bytes.NewReader(in.Data), size, in.MimeType); err != nil {
		return Uploaded{}, err
	}

	m := domain.Media{
		ID: id.String(), OwnerUserID: in.OwnerUserID, AdID: in.AdID,
		Bucket: s.blob.Bucket(), ObjectKey: objectKey, MimeType: in.MimeType,
		SizeBytes: size, Status: domain.StatusUploaded, CreatedAt: time.Now().UTC(),
	}
	ev, err := s.event(m)
	if err != nil {
		s.compensate(ctx, objectKey)
		return Uploaded{}, err
	}
	if err := s.store.InsertWithEvent(ctx, m, ev); err != nil {
		s.compensate(ctx, objectKey) // откат: объект уже в хранилище, а записи нет
		return Uploaded{}, err
	}
	return Uploaded{Media: m, PublicURL: s.blob.PublicURL(objectKey)}, nil
}

// Get возвращает медиа по id вместе с публичным URL.
func (s *Service) Get(ctx context.Context, id string) (Uploaded, error) {
	m, err := s.store.GetByID(ctx, id)
	if err != nil {
		return Uploaded{}, err
	}
	return Uploaded{Media: m, PublicURL: s.blob.PublicURL(m.ObjectKey)}, nil
}

// event собирает событие bozor.media.uploaded.
func (s *Service) event(m domain.Media) (events.Envelope, error) {
	ev, err := events.New(events.SubjectMediaUploaded, source, mediaEvent{
		MediaID: m.ID, OwnerUserID: m.OwnerUserID, AdID: m.AdID,
		Bucket: m.Bucket, ObjectKey: m.ObjectKey, MimeType: m.MimeType, SizeBytes: m.SizeBytes,
	})
	if err != nil {
		return events.Envelope{}, fmt.Errorf("app: сборка события: %w", err)
	}
	return ev, nil
}

// compensate удаляет уже загруженный объект, если запись в БД не удалась.
func (s *Service) compensate(ctx context.Context, objectKey string) {
	if err := s.blob.Remove(ctx, objectKey); err != nil {
		s.log.WarnContext(ctx, "не удалось удалить объект после сбоя вставки",
			slog.String("object_key", objectKey), slog.String("error", err.Error()))
	}
}

// mediaEvent — payload события bozor.media.uploaded (без PII).
type mediaEvent struct {
	MediaID     string  `json:"media_id"`
	OwnerUserID string  `json:"owner_user_id"`
	AdID        *string `json:"ad_id,omitempty"`
	Bucket      string  `json:"bucket"`
	ObjectKey   string  `json:"object_key"`
	MimeType    string  `json:"mime_type"`
	SizeBytes   int64   `json:"size_bytes"`
}

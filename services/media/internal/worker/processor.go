// Package worker содержит фоновые процессы Media-сервиса: обработку загруженных
// изображений (превью, удаление EXIF) по событиям шины и очистку сирот по TTL.
package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"

	"bozor/pkg/shared/events"

	"bozor/services/media/internal/domain"
	"bozor/services/media/internal/imaging"
)

// Consumer — имя durable-консьюмера и ключ inbox-идемпотентности.
const Consumer = "media-processor"

const source = "media"

// ProcessStore — операции БД, нужные обработчику (реализуется repo.Repo).
type ProcessStore interface {
	GetByID(ctx context.Context, id string) (domain.Media, error)
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, consumer, eventID string) error
	MarkProcessedWithEvent(ctx context.Context, consumer, eventID string, m domain.Media, ev events.Envelope) error
}

// Blob — объектное хранилище оригиналов и превью (реализуется storage.Store).
type Blob interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Remove(ctx context.Context, key string) error
}

// Processor обрабатывает событие bozor.media.uploaded: разворачивает оригинал
// по EXIF-ориентации, переписывает его без метаданных, генерирует превью
// (120/480/1080, без увеличения) и переводит медиа в статус ready.
type Processor struct {
	store ProcessStore
	blob  Blob
	log   *slog.Logger
}

// NewProcessor создаёт обработчик медиа.
func NewProcessor(store ProcessStore, blob Blob, log *slog.Logger) *Processor {
	return &Processor{store: store, blob: blob, log: log}
}

// uploadedPayload — интересующая часть полезной нагрузки bozor.media.uploaded.
type uploadedPayload struct {
	MediaID string `json:"media_id"`
}

// Handle обрабатывает одно событие. Ошибка приводит к повтору/DLQ (natsx),
// nil — к подтверждению. Идемпотентно: повторная доставка не выполняет работу
// дважды (inbox + переход только из статуса uploaded).
func (p *Processor) Handle(ctx context.Context, env events.Envelope) error {
	var pl uploadedPayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор события: %w", err)
	}
	if pl.MediaID == "" {
		return errors.New("worker: пустой media_id в событии")
	}

	processed, err := p.store.IsEventProcessed(ctx, Consumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	m, err := p.store.GetByID(ctx, pl.MediaID)
	if errors.Is(err, domain.ErrMediaNotFound) {
		// Медиа удалено раньше обработки — отметить событие и выйти.
		return p.store.MarkEventProcessed(ctx, Consumer, env.ID)
	}
	if err != nil {
		return err
	}
	if m.Status != domain.StatusUploaded {
		// Уже обработано или стало сиротой — работа не нужна.
		return p.store.MarkEventProcessed(ctx, Consumer, env.ID)
	}

	data, err := p.blob.Get(ctx, m.ObjectKey)
	if err != nil {
		return err
	}
	dec, err := imaging.Decode(data)
	if err != nil {
		return err // не декодируется → повтор/DLQ; сироту уберёт очистка по TTL
	}
	asPNG := domain.IsPNGSource(m.MimeType)

	// 1) Переписать оригинал без EXIF (приватность: убрать GPS и прочие метаданные).
	if err := p.putEncoded(ctx, m.ObjectKey, dec.Img, asPNG); err != nil {
		return err
	}

	// 2) Превью 120/480/1080 (вписыванием, без увеличения; дубли по размеру схлопываем).
	previews, err := p.generatePreviews(ctx, m, dec, asPNG)
	if err != nil {
		return err
	}

	// 3) Атомарно: media→ready (+ размеры/превью) + событие + inbox.
	w, h := dec.Width, dec.Height
	m.Width, m.Height, m.Previews = &w, &h, previews
	ev, err := events.New(events.SubjectMediaProcessed, source, processedPayload(m))
	if err != nil {
		return fmt.Errorf("worker: сборка события: %w", err)
	}
	return p.store.MarkProcessedWithEvent(ctx, Consumer, env.ID, m, ev)
}

// generatePreviews создаёт и загружает превью, схлопывая дубликаты по размеру
// (маленький оригинал не масштабируется вверх — несколько бакетов дают один результат).
func (p *Processor) generatePreviews(ctx context.Context, m domain.Media, dec imaging.Decoded, asPNG bool) ([]domain.Preview, error) {
	ext := domain.PreviewExt(m.MimeType)
	seen := make(map[string]bool)
	previews := make([]domain.Preview, 0, len(domain.PreviewSizes))
	for _, size := range domain.PreviewSizes {
		img := imaging.Fit(dec.Img, size)
		w, h := imaging.Dimensions(img)
		dim := fmt.Sprintf("%dx%d", w, h)
		if seen[dim] {
			continue
		}
		seen[dim] = true
		key := domain.PreviewKey(m.ID, size, ext)
		if err := p.putEncoded(ctx, key, img, asPNG); err != nil {
			return nil, err
		}
		previews = append(previews, domain.Preview{Size: size, Width: w, Height: h, ObjectKey: key})
	}
	return previews, nil
}

// putEncoded кодирует изображение и кладёт его в хранилище по ключу key.
func (p *Processor) putEncoded(ctx context.Context, key string, img image.Image, asPNG bool) error {
	enc, err := imaging.Encode(img, asPNG)
	if err != nil {
		return err
	}
	return p.blob.Put(ctx, key, bytes.NewReader(enc), int64(len(enc)), contentType(asPNG))
}

// contentType возвращает MIME-тип закодированного превью/оригинала.
func contentType(asPNG bool) string {
	if asPNG {
		return "image/png"
	}
	return "image/jpeg"
}

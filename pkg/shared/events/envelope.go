// Package events содержит envelope событий в стиле CloudEvents-lite
// и каталог NATS subjects для шины событий Bozor.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope — конверт события (упрощённый CloudEvents).
// Поле Type совпадает с NATS subject, на который публикуется событие.
type Envelope struct {
	// ID — уникальный идентификатор события (UUIDv7).
	ID string `json:"id"`
	// Type — тип события, совпадает с NATS subject.
	Type string `json:"type"`
	// Source — сервис-источник события.
	Source string `json:"source"`
	// SpecVersion — версия спецификации конверта ("1.0").
	SpecVersion string `json:"specversion"`
	// Time — время создания события (UTC).
	Time time.Time `json:"time"`
	// Data — полезная нагрузка события в JSON.
	Data json.RawMessage `json:"data"`
}

// New создаёт конверт события: генерирует UUIDv7, проставляет
// SpecVersion "1.0", текущее время в UTC и сериализует data в JSON.
func New(eventType, source string, data any) (Envelope, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("events: генерация UUID: %w", err)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: сериализация данных события: %w", err)
	}

	return Envelope{
		ID:          id.String(),
		Type:        eventType,
		Source:      source,
		SpecVersion: "1.0",
		Time:        time.Now().UTC(),
		Data:        raw,
	}, nil
}

// Decode десериализует полезную нагрузку события в dst.
func (e Envelope) Decode(dst any) error {
	if err := json.Unmarshal(e.Data, dst); err != nil {
		return fmt.Errorf("events: десериализация данных события: %w", err)
	}
	return nil
}

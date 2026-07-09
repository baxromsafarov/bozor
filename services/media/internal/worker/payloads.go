package worker

import "bozor/services/media/internal/domain"

// processedEvent — полезная нагрузка bozor.media.processed (без PII).
type processedEvent struct {
	MediaID     string        `json:"media_id"`
	OwnerUserID string        `json:"owner_user_id"`
	AdID        *string       `json:"ad_id,omitempty"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	Previews    []previewInfo `json:"previews"`
}

// previewInfo — описание одного превью в событии.
type previewInfo struct {
	Size      int    `json:"size"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ObjectKey string `json:"object_key"`
}

// deletedEvent — полезная нагрузка bozor.media.deleted (без PII).
type deletedEvent struct {
	MediaID     string  `json:"media_id"`
	OwnerUserID string  `json:"owner_user_id"`
	AdID        *string `json:"ad_id,omitempty"`
	ObjectKey   string  `json:"object_key"`
}

// processedPayload собирает нагрузку события bozor.media.processed из медиа.
func processedPayload(m domain.Media) processedEvent {
	ev := processedEvent{
		MediaID: m.ID, OwnerUserID: m.OwnerUserID, AdID: m.AdID,
		Previews: make([]previewInfo, 0, len(m.Previews)),
	}
	if m.Width != nil {
		ev.Width = *m.Width
	}
	if m.Height != nil {
		ev.Height = *m.Height
	}
	for _, p := range m.Previews {
		ev.Previews = append(ev.Previews, previewInfo{
			Size: p.Size, Width: p.Width, Height: p.Height, ObjectKey: p.ObjectKey,
		})
	}
	return ev
}

// deletedPayload собирает нагрузку события bozor.media.deleted из медиа.
func deletedPayload(m domain.Media) deletedEvent {
	return deletedEvent{
		MediaID: m.ID, OwnerUserID: m.OwnerUserID, AdID: m.AdID, ObjectKey: m.ObjectKey,
	}
}

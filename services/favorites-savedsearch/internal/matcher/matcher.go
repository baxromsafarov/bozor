// Package matcher сопоставляет новые (одобренные) объявления с сохранёнными
// поисками и публикует событие совпадения для Notification. Событие
// bozor.ad.approved — триггер; актуальное состояние объявления читается из
// Listing (источник истины); дедуп и троттлинг — в БД (Stage 5.3, ARCHITECTURE §4.8).
package matcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/favorites-savedsearch/internal/domain"
)

// Consumer — имя durable-консьюмера matcher'а.
const Consumer = "savedsearch-matcher"

const source = "favorites-savedsearch"

// Source — источник актуального состояния объявления (реализуется listingclient).
type Source interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// Store — операции matcher'а над БД (реализуется repo.Repo).
type Store interface {
	Candidates(ctx context.Context, categoryID string, regionID int16) ([]domain.SavedSearch, error)
	RecordMatchWithEvent(ctx context.Context, searchID, adID string, throttleSeconds int, ev events.Envelope) (bool, error)
}

// Matcher оценивает сохранённые поиски против одобренного объявления.
type Matcher struct {
	source   Source
	store    Store
	throttle time.Duration
	log      *slog.Logger
}

// New создаёт matcher.
func New(source Source, store Store, throttle time.Duration, log *slog.Logger) *Matcher {
	return &Matcher{source: source, store: store, throttle: throttle, log: log}
}

// adApprovedPayload — интересующая часть события bozor.ad.approved.
type adApprovedPayload struct {
	AdID string `json:"ad_id"`
}

// SavedSearchMatched — payload события bozor.saved_search.matched (для Notification).
type SavedSearchMatched struct {
	SavedSearchID string `json:"saved_search_id"`
	UserID        string `json:"user_id"`
	AdID          string `json:"ad_id"`
	Name          string `json:"name"`
}

// Handle обрабатывает одобрение объявления: читает его, отбирает кандидатов по
// грубым ключам, точно оценивает фильтры и публикует совпадения (дедуп+троттлинг
// в репозитории). Идемпотентно: повторная доставка не плодит уведомления.
func (m *Matcher) Handle(ctx context.Context, env events.Envelope) error {
	var pl adApprovedPayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("matcher: разбор bozor.ad.approved: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("matcher: пустой ad_id в bozor.ad.approved")
	}

	ad, found, err := m.source.GetAd(ctx, pl.AdID)
	if err != nil {
		return err
	}
	if !found {
		return nil // объявление удалено до обработки одобрения
	}

	candidates, err := m.store.Candidates(ctx, ad.CategoryID, ad.RegionID)
	if err != nil {
		return err
	}

	throttleSeconds := int(m.throttle.Seconds())
	notified := 0
	for _, ss := range candidates {
		if !ss.Query.Matches(ad) {
			continue
		}
		ev, err := events.New(events.SubjectSavedSearchMatched, source, SavedSearchMatched{
			SavedSearchID: ss.ID, UserID: ss.UserID, AdID: ad.ID, Name: ss.Name,
		})
		if err != nil {
			return fmt.Errorf("matcher: сборка события: %w", err)
		}
		published, err := m.store.RecordMatchWithEvent(ctx, ss.ID, ad.ID, throttleSeconds, ev)
		if err != nil {
			return err
		}
		if published {
			notified++
		}
	}
	if notified > 0 {
		m.log.InfoContext(ctx, "совпадения сохранённых поисков уведомлены",
			slog.String("ad_id", ad.ID), slog.Int("notified", notified),
			slog.Int("candidates", len(candidates)))
	}
	return nil
}

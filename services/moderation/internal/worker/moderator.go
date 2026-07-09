// Package worker содержит консьюмер авто-модерации: на отправленное на проверку
// объявление (bozor.ad.created|updated со статусом pending) читает его из Listing,
// прогоняет авто-проверки (стоп-слова, запрещённые категории, дубли) и либо
// авто-одобряет (bozor.ad.approved), либо ставит в ручную очередь (Stage 6.2).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/domain"
)

// Consumer — имя durable-консьюмера авто-модерации.
const Consumer = "moderation-auto"

const serviceSource = "moderation"

// statusPending — статус объявления, при котором оно подлежит модерации.
const statusPending = "pending"

// Source — источник актуального состояния объявления (реализуется listingclient).
type Source interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// Store — операции модерации над БД (реализуется repo.Repo).
type Store interface {
	AlreadyProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	ActiveStopwords(ctx context.Context) ([]string, error)
	IsForbiddenCategory(ctx context.Context, categoryID string) (bool, error)
	HasDuplicate(ctx context.Context, userID, contentHash, excludeAdID string) (bool, error)
	SaveApprovedTask(ctx context.Context, consumer, eventID string, t domain.Task, ev events.Envelope) error
	SaveManualTask(ctx context.Context, consumer, eventID string, t domain.Task) error
	MarkProcessed(ctx context.Context, consumer, eventID string) error
}

// Moderator — консьюмер авто-модерации.
type Moderator struct {
	source Source
	store  Store
	newID  func() (string, error)
	log    *slog.Logger
}

// New создаёт консьюмер авто-модерации.
func New(source Source, store Store, log *slog.Logger) *Moderator {
	return &Moderator{
		source: source,
		store:  store,
		newID:  func() (string, error) { id, err := uuid.NewV7(); return id.String(), err },
		log:    log,
	}
}

// adEvent — интересующая часть события жизненного цикла объявления.
type adEvent struct {
	AdID   string `json:"ad_id"`
	Status string `json:"status"`
}

// approvedPayload — payload bozor.ad.approved при авто-одобрении.
type approvedPayload struct {
	AdID   string `json:"ad_id"`
	UserID string `json:"user_id"`
	Title  string `json:"title"`
}

// Handle обрабатывает событие объявления: модерирует только отправленные на
// проверку (pending), читает объявление из Listing (источник истины), прогоняет
// авто-проверки и фиксирует решение. Идемпотентно по event_id (inbox).
func (m *Moderator) Handle(ctx context.Context, env events.Envelope) error {
	var pl adEvent
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор события объявления: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("worker: пустой ad_id в событии объявления")
	}
	if pl.Status != statusPending {
		return nil // модерируем только объявления, отправленные на проверку
	}

	processed, err := m.store.AlreadyProcessed(ctx, Consumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	ad, found, err := m.source.GetAd(ctx, pl.AdID)
	if err != nil {
		return err
	}
	if !found || ad.Status != statusPending {
		// Объявление удалено или уже вышло из pending — фиксируем обработку.
		return m.store.MarkProcessed(ctx, Consumer, env.ID)
	}

	reasons, err := m.autoCheck(ctx, ad)
	if err != nil {
		return err
	}

	taskID, err := m.newID()
	if err != nil {
		return fmt.Errorf("worker: генерация id задачи: %w", err)
	}
	task := domain.Task{
		ID: taskID, AdID: ad.ID, UserID: ad.UserID, Title: ad.Title,
		ContentHash: domain.ContentHash(ad.Title, ad.Description), Reasons: reasons,
	}

	if len(reasons) == 0 {
		task.Status = domain.StatusApproved
		task.AutoResult = domain.AutoPassed
		ev, err := events.New(events.SubjectAdApproved, serviceSource, approvedPayload{
			AdID: ad.ID, UserID: ad.UserID, Title: ad.Title,
		})
		if err != nil {
			return fmt.Errorf("worker: сборка bozor.ad.approved: %w", err)
		}
		if err := m.store.SaveApprovedTask(ctx, Consumer, env.ID, task, ev); err != nil {
			return err
		}
		m.log.InfoContext(ctx, "объявление авто-одобрено", slog.String("ad_id", ad.ID))
		return nil
	}

	task.Status = domain.StatusManual
	task.AutoResult = domain.AutoFlagged
	if err := m.store.SaveManualTask(ctx, Consumer, env.ID, task); err != nil {
		return err
	}
	m.log.InfoContext(ctx, "объявление отправлено в ручную очередь",
		slog.String("ad_id", ad.ID), slog.Any("reasons", reasons))
	return nil
}

// autoCheck прогоняет авто-проверки и возвращает причины флага (пусто — прошло).
func (m *Moderator) autoCheck(ctx context.Context, ad domain.AdView) ([]string, error) {
	stopwords, err := m.store.ActiveStopwords(ctx)
	if err != nil {
		return nil, err
	}
	matched := domain.MatchedStopwords(ad.Title, ad.Description, stopwords)

	forbidden, err := m.store.IsForbiddenCategory(ctx, ad.CategoryID)
	if err != nil {
		return nil, err
	}

	dup, err := m.store.HasDuplicate(ctx, ad.UserID, domain.ContentHash(ad.Title, ad.Description), ad.ID)
	if err != nil {
		return nil, err
	}

	return domain.Evaluate(matched, forbidden, dup), nil
}

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
)

// LifecycleConsumer — имя durable-консьюмера жизненного цикла объявлений.
const LifecycleConsumer = "payments-ad-lifecycle"

// LifecycleService — согласование сроков услуг с жизненным циклом объявления.
type LifecycleService interface {
	Suspend(ctx context.Context, adID string) (int, error)
	Resume(ctx context.Context, adID, title string) (bool, error)
	Refund(ctx context.Context, adID, reason string) (bool, error)
}

// Lifecycle потребляет события жизненного цикла объявления и приводит применённые
// услуги в соответствие (Stage 8.7): истечение → приостановка, реактивация →
// возобновление, снятие/удаление → пропорциональный возврат. Идемпотентно по
// эффекту (переходы статусов услуг защищены условиями UPDATE) — inbox не нужен.
type Lifecycle struct {
	svc LifecycleService
	log *slog.Logger
}

// NewLifecycle создаёт обработчик жизненного цикла объявлений.
func NewLifecycle(svc LifecycleService, log *slog.Logger) *Lifecycle {
	return &Lifecycle{svc: svc, log: log}
}

// adLifecyclePayload — нужные поля bozor.ad.* (AdEvent из Listing): id, статус
// (для реактивации) и заголовок (для восстановления события продвижения).
type adLifecyclePayload struct {
	AdID   string `json:"ad_id"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// Handle обрабатывает одно событие жизненного цикла. Ошибка ведёт к повтору/DLQ
// (natsx), nil — к подтверждению.
func (l *Lifecycle) Handle(ctx context.Context, env events.Envelope) error {
	var pl adLifecyclePayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор события жизненного цикла: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("worker: пустой ad_id в событии жизненного цикла")
	}

	switch env.Type {
	case events.SubjectAdExpired:
		n, err := l.svc.Suspend(ctx, pl.AdID)
		if err != nil {
			return err
		}
		if n > 0 {
			l.log.InfoContext(ctx, "услуги приостановлены (истечение объявления)",
				slog.String("ad_id", pl.AdID), slog.Int("count", n))
		}
	case events.SubjectAdBlocked, events.SubjectAdDeleted:
		done, err := l.svc.Refund(ctx, pl.AdID, refundReason(env.Type))
		if err != nil {
			return err
		}
		if done {
			l.log.InfoContext(ctx, "услуги завершены возвратом (снятие объявления)",
				slog.String("ad_id", pl.AdID), slog.String("reason", refundReason(env.Type)))
		}
	case events.SubjectAdUpdated:
		if pl.Status != domain.AdStatusActive {
			return nil // услуги возобновляются только при возврате объявления в active
		}
		resumed, err := l.svc.Resume(ctx, pl.AdID, pl.Title)
		if err != nil {
			return err
		}
		if resumed {
			l.log.InfoContext(ctx, "услуги возобновлены (реактивация объявления)",
				slog.String("ad_id", pl.AdID))
		}
	default:
		return fmt.Errorf("worker: неожиданный тип события %q", env.Type)
	}
	return nil
}

// refundReason сопоставляет событию причину возврата (для bozor.wallet.refunded).
func refundReason(subject string) string {
	if subject == events.SubjectAdDeleted {
		return "ad_deleted"
	}
	return "ad_blocked"
}

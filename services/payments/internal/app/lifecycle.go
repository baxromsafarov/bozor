package app

import (
	"context"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
)

// LifecycleRepo — операции над применёнными услугами при смене жизненного цикла
// объявления (истечение/снятие/удаление/реактивация).
type LifecycleRepo interface {
	ListAdPromotions(ctx context.Context, adID, status string) ([]domain.AdPromotion, error)
	SuspendPromotions(ctx context.Context, adID string, now time.Time) (int, error)
	ResumePromotions(ctx context.Context, adID string, now time.Time, ev *events.Envelope) (bool, error)
	RefundPromotions(ctx context.Context, adID, userID string, amount int64, reason string) (bool, error)
}

// LifecycleService согласует сроки платных услуг с жизненным циклом объявления
// (Stage 8.7): приостановка при истечении, возобновление при реактивации,
// пропорциональный возврат при снятии/удалении.
type LifecycleService struct {
	repo LifecycleRepo
	log  *slog.Logger
}

// NewLifecycleService создаёт сервис взаимодействия сроков.
func NewLifecycleService(repo LifecycleRepo, log *slog.Logger) *LifecycleService {
	return &LifecycleService{repo: repo, log: log}
}

// Suspend приостанавливает активные услуги объявления (истечение объявления):
// оплаченный срок замораживается до реактивации. Возвращает число приостановленных.
func (s *LifecycleService) Suspend(ctx context.Context, adID string) (int, error) {
	return s.repo.SuspendPromotions(ctx, adID, time.Now().UTC())
}

// Refund завершает услуги снятого/удалённого объявления, возвращая на кошелёк
// пропорциональную стоимость неиспользованного срока (одной транзакцией с
// переводом услуг в refunded). Возвращает false, если завершать нечего.
func (s *LifecycleService) Refund(ctx context.Context, adID, reason string) (bool, error) {
	promos, err := s.repo.ListAdPromotions(ctx, adID, "")
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	var (
		amount int64
		userID string
	)
	for _, p := range promos {
		if p.Status != domain.PromotionActive && p.Status != domain.PromotionSuspended {
			continue
		}
		userID = p.UserID // услуги объявления оплачены одним владельцем
		amount += domain.RefundAmount(p, now)
	}
	if userID == "" {
		return false, nil // нет незавершённых услуг
	}
	return s.repo.RefundPromotions(ctx, adID, userID, amount, reason)
}

// Resume возобновляет приостановленные услуги при реактивации объявления. Если
// среди них была TOP — восстанавливает продвижение событием
// bozor.promotion.activated с новым (сдвинутым на простой) сроком, чтобы Listing/
// Search вернули объявление в топ-блок. Возвращает false, если возобновлять нечего.
func (s *LifecycleService) Resume(ctx context.Context, adID, title string) (bool, error) {
	suspended, err := s.repo.ListAdPromotions(ctx, adID, domain.PromotionSuspended)
	if err != nil {
		return false, err
	}
	if len(suspended) == 0 {
		return false, nil
	}
	now := time.Now().UTC()
	return s.repo.ResumePromotions(ctx, adID, now, s.resumeTopEvent(ctx, adID, title, suspended, now))
}

// resumeTopEvent строит bozor.promotion.activated для возобновляемой TOP-услуги с
// новым сроком (сдвиг на длительность простоя). nil, если среди приостановленных
// нет TOP со сроком (для VIP/BUMP восстанавливать is_top не нужно).
func (s *LifecycleService) resumeTopEvent(ctx context.Context, adID, title string, suspended []domain.AdPromotion, now time.Time) *events.Envelope {
	for _, p := range suspended {
		if !domain.IsTopService(p.ServiceCode) || p.EndsAt == nil || p.SuspendedAt == nil {
			continue
		}
		newEnds := p.EndsAt.Add(now.Sub(*p.SuspendedAt))
		ev, err := events.New(events.SubjectPromotionActivated, "payments", promotionActivatedPayload{
			AdID: adID, UserID: p.UserID, Title: title, ServiceCode: p.ServiceCode,
			IsTop: true, PromotionRank: newEnds.Unix(), EndsAt: &newEnds,
		})
		if err != nil {
			s.log.ErrorContext(ctx, "возобновление TOP: сборка события",
				slog.String("ad_id", adID), slog.String("error", err.Error()))
			return nil
		}
		return &ev
	}
	return nil
}

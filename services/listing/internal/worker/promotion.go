package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// PromotionConsumer — имя durable-консьюмера активаций продвижения.
const PromotionConsumer = "listing-promotion"

// PromotionStore — операции БД, нужные обработчику активаций продвижения.
type PromotionStore interface {
	GetByID(ctx context.Context, id string) (domain.Ad, error)
	SetPromotionWithEvent(ctx context.Context, adID string, rank int32, endsAt *time.Time, ev events.Envelope) (bool, error)
}

// Promoter потребляет bozor.promotion.activated и проставляет промо-флаги
// объявлению в write-модели Listing (источник истины, ADR-043): is_top,
// promotion_rank и promo_ends_at. Публикует bozor.ad.updated, чтобы индексатор
// Search перечитал объявление (fetch-current-state) и добавил его в топ-блок.
// Идемпотентно по эффекту — inbox не нужен: UPDATE выставляет фиксированные
// значения, повторная доставка сводит объявление к тому же состоянию.
type Promoter struct {
	store PromotionStore
	log   *slog.Logger
}

// NewPromoter создаёт обработчик активаций продвижения.
func NewPromoter(store PromotionStore, log *slog.Logger) *Promoter {
	return &Promoter{store: store, log: log}
}

// promotionActivatedPayload — интересующая часть bozor.promotion.activated
// (payments, Stage 8.4). Топ-блок Search управляется только услугой TOP —
// не-TOP активации (VIP/BUMP/LOGO) для Listing холостые.
type promotionActivatedPayload struct {
	AdID          string     `json:"ad_id"`
	IsTop         bool       `json:"is_top"`
	PromotionRank int64      `json:"promotion_rank"`
	EndsAt        *time.Time `json:"ends_at"`
}

// Handle обрабатывает одну активацию продвижения. Ошибка ведёт к повтору/DLQ
// (natsx), nil — к подтверждению. Не-TOP активации и объявления, которых нет или
// они не активны, подтверждаются без изменений.
func (p *Promoter) Handle(ctx context.Context, env events.Envelope) error {
	var pl promotionActivatedPayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор активации продвижения: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("worker: пустой ad_id в активации продвижения")
	}
	if !pl.IsTop {
		return nil // на топ-блок влияет только TOP — прочее подтверждаем без работы
	}

	ad, err := p.store.GetByID(ctx, pl.AdID)
	if errors.Is(err, domain.ErrAdNotFound) {
		return nil // объявление удалено до применения — подтвердить и выйти
	}
	if err != nil {
		return err
	}

	// promotion_rank — unix-секунды окончания промо; помещаются в int32 до 2038
	// (тип поля promotion_rank в коллекции Search — int32).
	rank := int32(pl.PromotionRank) //nolint:gosec // unix-секунды промо укладываются в int32
	// Отражаем промо в копии для полезной нагрузки события источника истины.
	ad.IsTop = true
	ad.PromotionRank = rank
	ad.PromoEndsAt = pl.EndsAt
	ev, err := events.New(events.SubjectAdUpdated, source, app.NewAdEvent(ad))
	if err != nil {
		return fmt.Errorf("worker: сборка события: %w", err)
	}

	applied, err := p.store.SetPromotionWithEvent(ctx, pl.AdID, rank, pl.EndsAt, ev)
	if err != nil {
		return err
	}
	if applied {
		p.log.InfoContext(ctx, "продвижение TOP применено к объявлению",
			slog.String("ad_id", pl.AdID))
	}
	return nil
}

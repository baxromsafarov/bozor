package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/payments/internal/domain"
)

// paymentColumns — общий список колонок для сканирования платежа.
const paymentColumns = `id, user_id, provider, purpose, amount_uzs, status, external_id, created_at, updated_at`

// paymentEventPayload — полезная нагрузка событий bozor.payment.* (совместима с
// Notification: user_id — получатель, amount+currency — сумма).
type paymentEventPayload struct {
	PaymentID string `json:"payment_id"`
	UserID    string `json:"user_id"`
	Provider  string `json:"provider"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// CreatePayment создаёт платёж в статусе pending.
func (r *Repo) CreatePayment(ctx context.Context, p domain.Payment) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payments (id, user_id, provider, purpose, amount_uzs, status)
		VALUES ($1, $2, $3, $4, $5, 'pending')`,
		p.ID, p.UserID, p.Provider, p.Purpose, p.AmountUZS)
	return err
}

// GetPayment возвращает платёж по id (found=false, если нет).
func (r *Repo) GetPayment(ctx context.Context, id string) (domain.Payment, bool, error) {
	return scanPayment(r.pool.QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE id = $1`, id))
}

// GetPaymentByExternal возвращает платёж по (provider, external_id).
func (r *Repo) GetPaymentByExternal(ctx context.Context, provider, externalID string) (domain.Payment, bool, error) {
	return scanPayment(r.pool.QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE provider = $1 AND external_id = $2`, provider, externalID))
}

// SetExternalID привязывает id транзакции провайдера к платежу (идемпотентно:
// повторная привязка того же external_id не ошибка). Применяется на этапе
// создания транзакции у провайдера (Payme CreateTransaction / Click Prepare).
func (r *Repo) SetExternalID(ctx context.Context, id, externalID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE payments SET external_id = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending' AND (external_id IS NULL OR external_id = $2)`,
		id, externalID)
	return err
}

// ConfirmPayment идемпотентно подтверждает платёж: переводит pending→succeeded
// (условный UPDATE — защита от повторного колбэка), зачисляет средства на кошелёк
// и кладёт bozor.payment.succeeded в outbox — всё в одной транзакции. credited=true
// только если переход произошёл именно этим вызовом (повтор → false, без двойного
// зачисления).
func (r *Repo) ConfirmPayment(ctx context.Context, id, externalID string) (domain.Payment, bool, error) {
	var (
		p        domain.Payment
		credited bool
	)
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		got, found, err := scanPayment(tx.QueryRow(ctx, `
			UPDATE payments
			   SET status = 'succeeded', external_id = COALESCE($2, external_id), updated_at = now()
			 WHERE id = $1 AND status = 'pending'
			RETURNING `+paymentColumns, id, nullIfEmpty(externalID)))
		if err != nil {
			return err
		}
		if !found {
			// Переход не случился: платёж уже терминальный или отсутствует.
			cur, ok, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id = $1`, id))
			if err != nil {
				return err
			}
			if !ok {
				return domain.ErrPaymentNotFound
			}
			p = cur
			return nil
		}
		credited = true
		p = got
		if _, err := creditTx(ctx, tx, p.UserID, p.AmountUZS, domain.KindTopup, &p.ID); err != nil {
			return err
		}
		return enqueuePaymentEvent(ctx, tx, events.SubjectPaymentSucceeded, p)
	})
	if err != nil {
		return domain.Payment{}, false, err
	}
	return p, credited, nil
}

// FailPayment идемпотентно переводит pending→failed и кладёт bozor.payment.failed
// в outbox. changed=true только если переход произошёл этим вызовом.
func (r *Repo) FailPayment(ctx context.Context, id, status string) (domain.Payment, bool, error) {
	if status != domain.PaymentFailed && status != domain.PaymentCanceled {
		return domain.Payment{}, false, domain.ErrPaymentState
	}
	var (
		p       domain.Payment
		changed bool
	)
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		got, found, err := scanPayment(tx.QueryRow(ctx, `
			UPDATE payments SET status = $2, updated_at = now()
			 WHERE id = $1 AND status = 'pending'
			RETURNING `+paymentColumns, id, status))
		if err != nil {
			return err
		}
		if !found {
			cur, ok, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id = $1`, id))
			if err != nil {
				return err
			}
			if !ok {
				return domain.ErrPaymentNotFound
			}
			p = cur
			return nil
		}
		changed = true
		p = got
		return enqueuePaymentEvent(ctx, tx, events.SubjectPaymentFailed, p)
	})
	if err != nil {
		return domain.Payment{}, false, err
	}
	return p, changed, nil
}

// enqueuePaymentEvent кладёт событие платежа в outbox в рамках транзакции.
func enqueuePaymentEvent(ctx context.Context, tx pgx.Tx, subject string, p domain.Payment) error {
	ev, err := events.New(subject, "payments", paymentEventPayload{
		PaymentID: p.ID, UserID: p.UserID, Provider: p.Provider,
		Amount: p.AmountUZS, Currency: domain.CurrencyUZS,
	})
	if err != nil {
		return fmt.Errorf("событие %s: %w", subject, err)
	}
	return outbox.Enqueue(ctx, tx, ev)
}

// scanPayment сканирует строку платежа; pgx.ErrNoRows → found=false без ошибки.
func scanPayment(row pgx.Row) (domain.Payment, bool, error) {
	var p domain.Payment
	err := row.Scan(&p.ID, &p.UserID, &p.Provider, &p.Purpose, &p.AmountUZS, &p.Status,
		&p.ExternalID, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, false, nil
	}
	if err != nil {
		return domain.Payment{}, false, err
	}
	return p, true, nil
}

// nullIfEmpty возвращает nil для пустой строки (чтобы COALESCE сохранил прежний
// external_id при пустом аргументе).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

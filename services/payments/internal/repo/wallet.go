package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/pgxx"

	"bozor/services/payments/internal/domain"
)

// GetWallet возвращает кошелёк пользователя. Если кошелька ещё нет, отдаёт
// нулевой баланс (кошелёк создаётся лениво при первом пополнении).
func (r *Repo) GetWallet(ctx context.Context, userID string) (domain.Wallet, error) {
	w := domain.Wallet{UserID: userID}
	err := r.pool.QueryRow(ctx,
		`SELECT balance_uzs, version FROM wallets WHERE user_id = $1`, userID).
		Scan(&w.BalanceUZS, &w.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, nil
	}
	if err != nil {
		return domain.Wallet{}, err
	}
	return w, nil
}

// Credit зачисляет средства на кошелёк пользователя одной транзакцией: апсертит
// баланс (+amount, version+1) и пишет пару проводок дебет/кредит (леджер). Кошелёк
// создаётся, если его ещё не было.
func (r *Repo) Credit(ctx context.Context, userID string, amount int64, kind string, reference *string) (domain.Wallet, error) {
	if amount <= 0 {
		return domain.Wallet{}, domain.ErrInvalidAmount
	}
	w := domain.Wallet{UserID: userID}
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO wallets (user_id, balance_uzs, version)
			VALUES ($1, $2, 1)
			ON CONFLICT (user_id) DO UPDATE
			  SET balance_uzs = wallets.balance_uzs + EXCLUDED.balance_uzs,
			      version     = wallets.version + 1,
			      updated_at  = now()
			RETURNING balance_uzs, version`, userID, amount).
			Scan(&w.BalanceUZS, &w.Version); err != nil {
			return fmt.Errorf("апсерт кошелька: %w", err)
		}
		// Двойная запись: +amount на счёт пользователя, −amount с источника средств.
		return insertPostings(ctx, tx, uuid.New().String(), kind, reference, posting{
			account: domain.AccountUserWallet, walletUser: &userID, direction: domain.DirectionCredit, amount: amount,
		}, posting{
			account: domain.AccountExternalTopup, walletUser: nil, direction: domain.DirectionDebit, amount: amount,
		})
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return w, nil
}

// Debit списывает средства с кошелька пользователя одной транзакцией: блокирует
// строку (FOR UPDATE), проверяет достаточность средств, уменьшает баланс
// (version+1) и пишет пару проводок. Недостаточно средств → ErrInsufficientFunds.
func (r *Repo) Debit(ctx context.Context, userID string, amount int64, kind string, reference *string) (domain.Wallet, error) {
	if amount <= 0 {
		return domain.Wallet{}, domain.ErrInvalidAmount
	}
	w := domain.Wallet{UserID: userID}
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var balance, version int64
		err := tx.QueryRow(ctx,
			`SELECT balance_uzs, version FROM wallets WHERE user_id = $1 FOR UPDATE`, userID).
			Scan(&balance, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrInsufficientFunds // кошелька нет — баланс 0
		}
		if err != nil {
			return fmt.Errorf("блокировка кошелька: %w", err)
		}
		if balance < amount {
			return domain.ErrInsufficientFunds
		}
		if err := tx.QueryRow(ctx, `
			UPDATE wallets
			   SET balance_uzs = balance_uzs - $2, version = version + 1, updated_at = now()
			 WHERE user_id = $1
			RETURNING balance_uzs, version`, userID, amount).
			Scan(&w.BalanceUZS, &w.Version); err != nil {
			return fmt.Errorf("списание с кошелька: %w", err)
		}
		// Двойная запись: −amount со счёта пользователя, +amount в выручку.
		return insertPostings(ctx, tx, uuid.New().String(), kind, reference, posting{
			account: domain.AccountUserWallet, walletUser: &userID, direction: domain.DirectionDebit, amount: amount,
		}, posting{
			account: domain.AccountRevenue, walletUser: nil, direction: domain.DirectionCredit, amount: amount,
		})
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return w, nil
}

// ListTransactions возвращает историю кошелька пользователя (свежие сверху) —
// только проводки его счёта.
func (r *Repo) ListTransactions(ctx context.Context, userID string, limit int) ([]domain.Transaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, operation_id, kind, direction, amount_uzs, reference, created_at
		FROM wallet_transactions
		WHERE wallet_user_id = $1
		ORDER BY created_at DESC, id
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Transaction
	for rows.Next() {
		var t domain.Transaction
		if err := rows.Scan(&t.ID, &t.OperationID, &t.Kind, &t.Direction, &t.AmountUZS,
			&t.Reference, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// posting — одна проводка операции.
type posting struct {
	account    string
	walletUser *string
	direction  string
	amount     int64
}

// insertPostings пишет парные проводки одной операции (append-only леджер).
func insertPostings(ctx context.Context, tx pgx.Tx, operationID, kind string, reference *string, ps ...posting) error {
	for _, p := range ps {
		if _, err := tx.Exec(ctx, `
			INSERT INTO wallet_transactions
			  (id, operation_id, kind, account, wallet_user_id, direction, amount_uzs, reference)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.New().String(), operationID, kind, p.account, p.walletUser, p.direction, p.amount, reference); err != nil {
			return fmt.Errorf("проводка леджера: %w", err)
		}
	}
	return nil
}

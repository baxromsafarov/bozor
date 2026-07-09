//go:build integration

package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/repo"
)

// TestCredit_CreatesWalletAndPairedPostings — пополнение создаёт кошелёк, ставит
// баланс и пишет пару проводок (сумма проводок операции = 0).
func TestCredit_CreatesWalletAndPairedPostings(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := uuid.NewString()

	w, err := r.Credit(ctx, userID, 50000, domain.KindTopup, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 50000, w.BalanceUZS)
	assert.EqualValues(t, 1, w.Version)

	// Две проводки в одной операции; кредит/дебет равны (баланс операции = 0).
	var (
		nPostings int
		credit    int64
		debit     int64
		nOps      int
	)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT operation_id) FROM wallet_transactions`).Scan(&nPostings, &nOps))
	assert.Equal(t, 2, nPostings, "пара проводок")
	assert.Equal(t, 1, nOps, "одна операция")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT coalesce(sum(amount_uzs) FILTER (WHERE direction='credit'),0), coalesce(sum(amount_uzs) FILTER (WHERE direction='debit'),0) FROM wallet_transactions`).
		Scan(&credit, &debit))
	assert.Equal(t, credit, debit, "двойная запись: кредит = дебет")

	// Проводка счёта пользователя несёт wallet_user_id, системная — NULL.
	var userPostings int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions WHERE wallet_user_id=$1`, userID).Scan(&userPostings))
	assert.Equal(t, 1, userPostings, "одна проводка на счёт пользователя")
}

// TestCredit_Accumulates — повторные пополнения складываются, версия растёт.
func TestCredit_Accumulates(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := uuid.NewString()

	_, err := r.Credit(ctx, userID, 30000, domain.KindTopup, nil)
	require.NoError(t, err)
	w, err := r.Credit(ctx, userID, 20000, domain.KindTopup, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 50000, w.BalanceUZS)
	assert.EqualValues(t, 2, w.Version)
}

// TestDebit_ReducesBalance_LedgerInvariant — списание уменьшает баланс, а баланс
// всегда равен сумме проводок счёта пользователя (Σ credit − Σ debit).
func TestDebit_ReducesBalance_LedgerInvariant(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := uuid.NewString()

	_, err := r.Credit(ctx, userID, 50000, domain.KindTopup, nil)
	require.NoError(t, err)
	ref := "promo-1"
	w, err := r.Debit(ctx, userID, 30000, domain.KindPurchase, &ref)
	require.NoError(t, err)
	assert.EqualValues(t, 20000, w.BalanceUZS)

	// Инвариант: баланс = Σ(credit) − Σ(debit) по проводкам пользователя.
	var ledger int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT coalesce(sum(amount_uzs) FILTER (WHERE direction='credit'),0)
		     - coalesce(sum(amount_uzs) FILTER (WHERE direction='debit'),0)
		FROM wallet_transactions WHERE wallet_user_id=$1`, userID).Scan(&ledger))
	assert.EqualValues(t, 20000, ledger, "баланс сходится с леджером")

	// Ссылка сохранена на проводках операции списания.
	var refCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions WHERE reference=$1`, ref).Scan(&refCount))
	assert.Equal(t, 2, refCount, "reference на обеих проводках операции")
}

// TestDebit_InsufficientFunds — списание больше баланса и с пустого кошелька
// отклоняется, баланс не меняется.
func TestDebit_InsufficientFunds(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := uuid.NewString()

	// Пустой кошелёк (строки нет) — недостаточно средств.
	_, err := r.Debit(ctx, userID, 1000, domain.KindPurchase, nil)
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

	_, err = r.Credit(ctx, userID, 5000, domain.KindTopup, nil)
	require.NoError(t, err)
	_, err = r.Debit(ctx, userID, 6000, domain.KindPurchase, nil)
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

	// Баланс не изменился, лишних проводок нет.
	w, err := r.GetWallet(ctx, userID)
	require.NoError(t, err)
	assert.EqualValues(t, 5000, w.BalanceUZS)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions WHERE wallet_user_id=$1`, userID).Scan(&n))
	assert.Equal(t, 1, n, "только проводка успешного пополнения")
}

// TestGetWallet_MissingReturnsZero — у нового пользователя кошелёк нулевой.
func TestGetWallet_MissingReturnsZero(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	w, err := r.GetWallet(ctx, uuid.NewString())
	require.NoError(t, err)
	assert.EqualValues(t, 0, w.BalanceUZS)
	assert.EqualValues(t, 0, w.Version)
}

// TestListTransactions_NewestFirst — история пользователя отдаётся свежими сверху
// с корректными направлением и видом.
func TestListTransactions_NewestFirst(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := uuid.NewString()

	_, err := r.Credit(ctx, userID, 50000, domain.KindTopup, nil)
	require.NoError(t, err)
	_, err = r.Debit(ctx, userID, 30000, domain.KindPurchase, nil)
	require.NoError(t, err)

	txs, err := r.ListTransactions(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, txs, 2, "только проводки счёта пользователя")
	assert.Equal(t, domain.DirectionDebit, txs[0].Direction, "свежая операция (списание) сверху")
	assert.Equal(t, domain.KindPurchase, txs[0].Kind)
	assert.Equal(t, domain.DirectionCredit, txs[1].Direction)
	assert.EqualValues(t, -30000, txs[0].SignedAmount())
	assert.EqualValues(t, 50000, txs[1].SignedAmount())
}

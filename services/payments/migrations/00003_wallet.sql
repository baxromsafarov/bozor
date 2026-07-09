-- +goose Up
-- Кошелёк пользователя (Stage 8.2). balance_uzs — денормализованный баланс
-- (кеш для быстрого чтения), источник истины — леджер wallet_transactions:
-- balance = Σ(credit) − Σ(debit) по проводкам счёта пользователя. version —
-- оптимистичная конкуренция (ARCHITECTURE §5), растёт на каждую мутацию.
CREATE TABLE wallets (
    user_id     uuid PRIMARY KEY,
    balance_uzs bigint      NOT NULL DEFAULT 0 CHECK (balance_uzs >= 0),
    version     bigint      NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Леджер в стиле двойной записи: каждая операция (operation_id) — пара проводок
-- дебет/кредит с равными суммами (сумма проводок операции = 0). Проводка счёта
-- пользователя несёт wallet_user_id (account='user_wallet'); парная системная
-- проводка (external_topup / promotion_revenue / refund_source) — с NULL. История
-- неизменяемая (append-only): строки не обновляются и не удаляются.
CREATE TABLE wallet_transactions (
    id             uuid PRIMARY KEY,
    operation_id   uuid        NOT NULL,
    kind           text        NOT NULL CHECK (kind IN ('topup', 'purchase', 'refund')),
    account        text        NOT NULL,
    wallet_user_id uuid,                     -- заполнено для проводок счёта пользователя; NULL — системный счёт
    direction      text        NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount_uzs     bigint      NOT NULL CHECK (amount_uzs > 0),
    reference      text,                     -- ссылка на связанную сущность (payment/ad_promotion) — заполняется в 8.3/8.4
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- История кошелька пользователя (свежие сверху) — только его проводки.
CREATE INDEX idx_wallet_tx_user ON wallet_transactions (wallet_user_id, created_at DESC)
    WHERE wallet_user_id IS NOT NULL;
-- Выборка парных проводок одной операции.
CREATE INDEX idx_wallet_tx_operation ON wallet_transactions (operation_id);

-- +goose Down
DROP TABLE wallet_transactions;
DROP TABLE wallets;

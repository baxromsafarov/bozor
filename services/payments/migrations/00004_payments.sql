-- +goose Up
-- Платежи через провайдеров (Stage 8.3). Пополнение кошелька идёт через
-- Payme/Click/Mock: создаётся платёж (pending), провайдер подтверждает колбэком →
-- зачисление на кошелёк. external_id — id транзакции у провайдера (появляется в
-- колбэке); частичный UNIQUE (provider, external_id) обеспечивает идемпотентность
-- (провайдеры ретраят колбэки — один external_id ↔ один платёж).
CREATE TABLE payments (
    id          uuid PRIMARY KEY,
    user_id     uuid        NOT NULL,
    provider    text        NOT NULL CHECK (provider IN ('mock', 'payme', 'click')),
    purpose     text        NOT NULL CHECK (purpose IN ('topup')),
    amount_uzs  bigint      NOT NULL CHECK (amount_uzs > 0),
    status      text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed', 'canceled')),
    external_id text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_payments_user ON payments (user_id, created_at DESC);
CREATE UNIQUE INDEX idx_payments_provider_external ON payments (provider, external_id)
    WHERE external_id IS NOT NULL;

-- Transactional outbox: публикация bozor.payment.succeeded|failed → Notification
-- (первый издатель событий в сервисе payments; появляется в 8.3).
CREATE TABLE outbox (
    id           uuid PRIMARY KEY,
    subject      text  NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE payments;

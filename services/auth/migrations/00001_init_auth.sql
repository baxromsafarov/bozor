-- +goose Up
-- Идентичность пользователя (телефон подтверждается через Telegram request_contact).
CREATE TABLE users (
    id               uuid PRIMARY KEY,
    telegram_user_id bigint      NOT NULL UNIQUE,
    phone            text        NOT NULL,
    username         text,
    first_name       text,
    last_name        text,
    language_code    text        NOT NULL DEFAULT 'ru',
    status           text        NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'banned', 'deleted')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_phone ON users (phone);

-- Refresh-токены: хранится только хеш; ротация + семейства для reuse-detection.
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL UNIQUE,
    device_id  text        NOT NULL,
    family_id  uuid        NOT NULL,
    revoked_at timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user   ON refresh_tokens (user_id);
CREATE INDEX idx_refresh_tokens_family ON refresh_tokens (family_id);

-- Аудит авторизаций: вход/выход/ротация/reuse-detection и т.п.
CREATE TABLE auth_audit_log (
    id         uuid PRIMARY KEY,
    user_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    event      text        NOT NULL,
    detail     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_audit_user ON auth_audit_log (user_id, created_at);

-- Transactional outbox (DDL синхронизирован с pkg/shared/outbox.MigrationSQL).
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
DROP TABLE auth_audit_log;
DROP TABLE refresh_tokens;
DROP TABLE users;

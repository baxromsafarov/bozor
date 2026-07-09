-- +goose Up
-- Проекция получателей: единственный источник telegram_user_id — событие
-- bozor.user.created (Auth). Notification держит локальную проекцию user_id →
-- Telegram chat_id + язык, чтобы адресовать доставку без синхронного вызова Auth.
CREATE TABLE recipients (
    user_id          uuid PRIMARY KEY,
    telegram_user_id bigint      NOT NULL,   -- chat_id для Bot API
    language_code    text        NOT NULL DEFAULT 'ru',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Журнал уведомлений и их статусов доставки. Идемпотентность по event_id:
-- одно доменное событие → не более одной доставки (защита от повторов шины).
-- status: pending (в процессе), sent (доставлено), failed (постоянная ошибка
-- канала), skipped (отключено настройками / нет получателя / канал выключен).
CREATE TABLE notifications (
    id           uuid PRIMARY KEY,
    event_id     uuid        NOT NULL UNIQUE,          -- ключ идемпотентности (id конверта события)
    user_id      uuid        NOT NULL,
    event_type   text        NOT NULL,                 -- NATS subject, напр. bozor.ad.approved
    channel      text        NOT NULL DEFAULT 'telegram',
    payload_json jsonb       NOT NULL DEFAULT '{}'::jsonb,
    body         text        NOT NULL DEFAULT '',      -- отрендеренный текст (аудит)
    status       text        NOT NULL,                 -- pending | sent | failed | skipped
    reason       text,                                 -- причина skipped/failed
    attempts     int         NOT NULL DEFAULT 0,       -- число попыток доставки
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz
);

-- Лента уведомлений пользователя (история/статусы доставки).
CREATE INDEX idx_notifications_user ON notifications (user_id, created_at DESC);

-- +goose Down
DROP TABLE notifications;
DROP TABLE recipients;

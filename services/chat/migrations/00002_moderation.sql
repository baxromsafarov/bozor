-- +goose Up
-- Снятое модератором сообщение: тело скрывается, blocked_at фиксирует момент (Stage 7.4).
ALTER TABLE messages ADD COLUMN blocked_at timestamptz;

-- Заблокированные (забаненные) пользователи — проекция из bozor.user.banned.
-- Забаненный не может писать и начинать диалоги. Разбан — удаление строки (на будущее).
CREATE TABLE banned_users (
    user_id   uuid PRIMARY KEY,
    reason    text        NOT NULL DEFAULT '',
    banned_at timestamptz NOT NULL DEFAULT now()
);

-- Inbox-идемпотентность консьюмеров чата (bozor.user.banned,
-- bozor.chat.message_blocked). DDL синхронизирован с pkg/shared/outbox.
CREATE TABLE processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose Down
DROP TABLE processed_events;
DROP TABLE banned_users;
ALTER TABLE messages DROP COLUMN blocked_at;

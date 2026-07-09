-- +goose Up
-- Результат обработки медиа воркером (Stage 3.2): размеры оригинала,
-- момент обработки и список сгенерированных превью (120/480/1080).
ALTER TABLE media ADD COLUMN processed_at timestamptz;
ALTER TABLE media ADD COLUMN previews jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Inbox-идемпотентность потребителей событий (DDL синхронизирован с
-- pkg/shared/outbox.InboxMigrationSQL). Media-сервис — первый консьюмер:
-- воркер обработки потребляет bozor.media.uploaded «ровно один раз».
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose Down
DROP TABLE processed_events;
ALTER TABLE media DROP COLUMN previews;
ALTER TABLE media DROP COLUMN processed_at;

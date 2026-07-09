-- +goose Up
-- Inbox-идемпотентность потребителя решений модерации (DDL синхронизирован с
-- pkg/shared/outbox.InboxMigrationSQL). Listing потребляет bozor.ad.approved|
-- rejected от Moderation и переводит объявление в active|rejected «ровно один раз».
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose Down
DROP TABLE processed_events;

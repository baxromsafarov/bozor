-- +goose Up
-- Загруженные медиафайлы. Оригинал лежит в MinIO по object_key; превью
-- (120/480/1080) и размеры проставляет воркер обработки (Stage 3.2).
-- ad_id — необязательная привязка к объявлению-черновику (FK нет: media —
-- отдельный сервис/БД; связь с объявлением финализируется в ad_images, 3.3).
CREATE TABLE media (
    id            uuid PRIMARY KEY,
    owner_user_id uuid        NOT NULL,
    ad_id         uuid,
    bucket        text        NOT NULL,
    object_key    text        NOT NULL UNIQUE,
    mime_type     text        NOT NULL,
    size_bytes    bigint      NOT NULL CHECK (size_bytes > 0),
    status        text        NOT NULL DEFAULT 'uploaded'
                  CHECK (status IN ('uploaded', 'ready', 'orphan')),
    width         integer,
    height        integer,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Подсчёт/выборка медиа по объявлению (лимит на объявление) и по владельцу.
CREATE INDEX idx_media_ad ON media (ad_id) WHERE ad_id IS NOT NULL;
CREATE INDEX idx_media_owner ON media (owner_user_id);

-- Transactional outbox (DDL синхронизирован с pkg/shared/outbox).
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
DROP TABLE media;

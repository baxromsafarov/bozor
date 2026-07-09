-- +goose Up
-- Объявления — источник истины (write-модель). Значения атрибутов валидируются
-- против Catalog (gRPC) на уровне приложения; категория/регион/медиа живут в
-- других сервисах/БД — поэтому кросс-БД FK на них НЕТ (db-per-service).
-- Деньги: price в тийинах (int64), валюта хранится явно (ADR-009).
CREATE TABLE ads (
    id            uuid PRIMARY KEY,
    user_id       uuid        NOT NULL,
    category_id   uuid        NOT NULL,
    title         text        NOT NULL,
    description   text        NOT NULL DEFAULT '',
    price         bigint      NOT NULL CHECK (price >= 0),   -- тийины
    currency      text        NOT NULL DEFAULT 'UZS',
    region_id     smallint    NOT NULL,
    city_id       bigint,
    lat           double precision,
    lng           double precision,
    status        text        NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft','pending','active','rejected','sold','expired','archived','blocked')),
    phone_display boolean     NOT NULL DEFAULT true,
    contact_prefs jsonb       NOT NULL DEFAULT '{}'::jsonb,
    published_at  timestamptz,
    expires_at    timestamptz,
    bumped_at     timestamptz,
    views_count   bigint      NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Лента моих объявлений по статусам и активная лента.
CREATE INDEX idx_ads_user_status ON ads (user_id, status);
CREATE INDEX idx_ads_status_published ON ads (status, published_at DESC);
CREATE INDEX idx_ads_category ON ads (category_id);
-- Воркер истечения (Stage 3.4) выбирает активные с истёкшим сроком.
CREATE INDEX idx_ads_expires ON ads (expires_at) WHERE status = 'active';

-- Значения атрибутов объявления. Ключ — slug атрибута из Catalog; значение —
-- текстом (типизация проверяется приложением по типу атрибута из Catalog).
CREATE TABLE ad_attribute_values (
    ad_id          uuid NOT NULL REFERENCES ads (id) ON DELETE CASCADE,
    attribute_slug text NOT NULL,
    value          text NOT NULL,
    PRIMARY KEY (ad_id, attribute_slug)
);

-- Привязка изображений (media_id из Media-сервиса; кросс-БД FK нет). Ровно одна
-- обложка на объявление обеспечивается частичным уникальным индексом.
CREATE TABLE ad_images (
    ad_id      uuid    NOT NULL REFERENCES ads (id) ON DELETE CASCADE,
    media_id   uuid    NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    is_cover   boolean NOT NULL DEFAULT false,
    PRIMARY KEY (ad_id, media_id)
);

CREATE UNIQUE INDEX idx_ad_images_cover ON ad_images (ad_id) WHERE is_cover;
CREATE INDEX idx_ad_images_order ON ad_images (ad_id, sort_order);

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
DROP TABLE ad_images;
DROP TABLE ad_attribute_values;
DROP TABLE ads;

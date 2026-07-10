-- +goose Up
-- Отзывы о продавце (Stage 9.1, ADR-045). Автор (покупатель) оценивает владельца
-- объявления (продавца) в контексте взаимодействия — объявления. Кросс-БД FK на
-- объявление/пользователей нет (db-per-service); владелец/статус объявления
-- читаются из Listing по fetch-current-state.
CREATE TABLE reviews (
    id         uuid PRIMARY KEY,
    ad_id      uuid        NOT NULL,                       -- взаимодействие (объявление)
    author_id  uuid        NOT NULL,                       -- автор отзыва (покупатель)
    target_id  uuid        NOT NULL,                       -- адресат (продавец = владелец объявления)
    rating     smallint    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body       text        NOT NULL DEFAULT '',
    status     text        NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'blocked')),    -- blocked — снят модератором
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Один отзыв на взаимодействие (анти-спам): автор оставляет не более одного
    -- отзыва по объявлению.
    UNIQUE (ad_id, author_id)
);

-- Лента отзывов о пользователе (свежие сверху) и агрегация рейтинга (9.2).
CREATE INDEX idx_reviews_target ON reviews (target_id, created_at DESC);

-- Transactional outbox (DDL синхронизирован с pkg/shared/outbox).
CREATE TABLE outbox (
    id           uuid PRIMARY KEY,
    subject      text  NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Inbox-идемпотентность консьюмера снятия отзывов (bozor.review.blocked).
CREATE TABLE processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose Down
DROP TABLE processed_events;
DROP TABLE outbox;
DROP TABLE reviews;

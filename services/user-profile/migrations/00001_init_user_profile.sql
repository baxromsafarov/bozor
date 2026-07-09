-- +goose Up
-- Расширенный профиль пользователя. Идентичность (телефон/telegram) — в Auth;
-- здесь — отображаемые/контактные данные, тип аккаунта, кеш рейтинга. user_id
-- приходит из bozor.user.created (Auth); кросс-БД FK на users НЕТ (db-per-service).
CREATE TABLE profiles (
    user_id               uuid PRIMARY KEY,
    display_name          text        NOT NULL DEFAULT '',
    avatar_media_id       uuid,
    about                 text        NOT NULL DEFAULT '',
    user_type             text        NOT NULL DEFAULT 'individual'
                          CHECK (user_type IN ('individual','business')),
    business_name         text        NOT NULL DEFAULT '',
    city_id               bigint,
    contact_phone_visible boolean     NOT NULL DEFAULT true,
    language_code         text        NOT NULL DEFAULT 'ru',
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- Настройки уведомлений: включённость по (канал, тип события). Отсутствие строки
-- трактуется приложением как «включено по умолчанию»; PUT сохраняет явный набор.
CREATE TABLE notification_prefs (
    user_id    uuid    NOT NULL REFERENCES profiles (user_id) ON DELETE CASCADE,
    channel    text    NOT NULL,
    event_type text    NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, channel, event_type)
);

-- Кеш агрегированного рейтинга продавца — обновляется по bozor.review.created
-- (Reviews, CHAPTER 9). До появления отзывов пусто → рейтинг читается как 0/0.
CREATE TABLE user_ratings_cache (
    user_id       uuid PRIMARY KEY REFERENCES profiles (user_id) ON DELETE CASCADE,
    avg_rating    numeric(3,2) NOT NULL DEFAULT 0,
    reviews_count integer      NOT NULL DEFAULT 0,
    updated_at    timestamptz  NOT NULL DEFAULT now()
);

-- Transactional outbox (DDL синхронизирован с pkg/shared/outbox): публикация
-- bozor.user.updated при изменении профиля.
CREATE TABLE outbox (
    id           uuid PRIMARY KEY,
    subject      text  NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Inbox-идемпотентность потребителей (DDL синхронизирован с pkg/shared/outbox):
-- bozor.user.created (создание профиля), в будущем bozor.review.created.
CREATE TABLE processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose Down
DROP TABLE processed_events;
DROP TABLE outbox;
DROP TABLE user_ratings_cache;
DROP TABLE notification_prefs;
DROP TABLE profiles;

-- +goose Up
-- Задачи модерации: одна актуальная задача на объявление (ad_id UNIQUE);
-- повторная модерация правки перезаписывает задачу. status: approved
-- (авто-одобрено), manual (не прошло авто-проверки → ручная очередь 6.3),
-- rejected (ручное отклонение 6.3). auto_result: passed | flagged.
CREATE TABLE moderation_tasks (
    id           uuid PRIMARY KEY,
    ad_id        uuid        NOT NULL UNIQUE,
    user_id      uuid        NOT NULL,
    title        text        NOT NULL DEFAULT '',
    content_hash text        NOT NULL DEFAULT '',   -- нормализованный хэш title+description (детекция дублей)
    status       text        NOT NULL,              -- approved | manual | rejected
    auto_result  text        NOT NULL,              -- passed | flagged
    reasons      jsonb       NOT NULL DEFAULT '[]'::jsonb, -- сработавшие авто-проверки
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Ручная очередь (status=manual) в порядке поступления.
CREATE INDEX idx_moderation_tasks_status ON moderation_tasks (status, created_at DESC);
-- Детекция дублей: объявления одного пользователя с тем же содержимым.
CREATE INDEX idx_moderation_tasks_dup ON moderation_tasks (user_id, content_hash);

-- Стоп-слова (uz/ru), управляются администратором. Минимальный словарь сидируется
-- ниже; проверка — подстрока без регистра по title+description объявления.
CREATE TABLE stopwords (
    id         uuid PRIMARY KEY,
    word       text        NOT NULL,
    lang       text        NOT NULL DEFAULT 'ru',
    active     boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (word, lang)
);

-- Запрещённые категории товаров (пусто по умолчанию; наполняет администратор).
CREATE TABLE forbidden_categories (
    category_id uuid PRIMARY KEY,
    reason      text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Inbox-идемпотентность консьюмера (DDL синхронизирован с pkg/shared/outbox).
CREATE TABLE processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- Transactional outbox: публикация bozor.ad.approved (авто-одобрение) → Listing/Search/Notification.
CREATE TABLE outbox (
    id           uuid PRIMARY KEY,
    subject      text  NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Сид базового словаря стоп-слов (ARCHITECTURE §4.11): «копия/реплика/подделка/
-- под оригинал» + узбекские аналоги. Идемпотентно.
INSERT INTO stopwords (id, word, lang) VALUES
    (gen_random_uuid(), 'копия', 'ru'),
    (gen_random_uuid(), 'реплика', 'ru'),
    (gen_random_uuid(), 'подделка', 'ru'),
    (gen_random_uuid(), 'под оригинал', 'ru'),
    (gen_random_uuid(), 'nusxa', 'uz'),
    (gen_random_uuid(), 'replika', 'uz'),
    (gen_random_uuid(), 'soxta', 'uz')
ON CONFLICT (word, lang) DO NOTHING;

-- +goose Down
DROP TABLE outbox;
DROP TABLE processed_events;
DROP TABLE forbidden_categories;
DROP TABLE stopwords;
DROP TABLE moderation_tasks;

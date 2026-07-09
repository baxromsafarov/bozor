-- +goose Up
-- Сохранённые поиски пользователя. query_json — полный набор фильтров (зеркалит
-- Search 4.3); category_id/region_id продублированы отдельными столбцами как
-- ГРУБЫЕ ключи для селективного отбора кандидатов matcher'ом (индекс), тонкая
-- оценка (цена/город/атрибуты/текст) — в приложении. NULL в грубом ключе = «любой».
CREATE TABLE saved_searches (
    id               uuid PRIMARY KEY,
    user_id          uuid        NOT NULL,
    name             text        NOT NULL,
    query_json       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    category_id      uuid,            -- грубый ключ (из query_json), NULL = любой
    region_id        smallint,        -- грубый ключ (из query_json), NULL = любой
    notify_enabled   boolean     NOT NULL DEFAULT true,
    last_notified_at timestamptz,     -- защита от лавины: троттлинг уведомлений
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- Лента сохранённых поисков пользователя.
CREATE INDEX idx_saved_searches_user ON saved_searches (user_id, created_at DESC);
-- Отбор кандидатов matcher'ом по грубым ключам (только notify_enabled).
CREATE INDEX idx_saved_searches_match ON saved_searches (category_id, region_id)
    WHERE notify_enabled;

-- Дедуп совпадений: пара (сохранённый поиск, объявление) уведомляется не более
-- одного раза (ключ дедупликации; ARCHITECTURE §4.8).
CREATE TABLE saved_search_matches (
    saved_search_id uuid        NOT NULL REFERENCES saved_searches (id) ON DELETE CASCADE,
    ad_id           uuid        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (saved_search_id, ad_id)
);

-- Transactional outbox (DDL синхронизирован с pkg/shared/outbox): публикация
-- bozor.saved_search.matched для Notification.
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
DROP TABLE saved_search_matches;
DROP TABLE saved_searches;

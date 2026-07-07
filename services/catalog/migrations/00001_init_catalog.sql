-- +goose Up
-- Дерево категорий: parent_id (self-FK), материализованный путь по slug,
-- уровень вложенности. Удаление категории с детьми запрещено (RESTRICT).
CREATE TABLE categories (
    id         uuid PRIMARY KEY,
    parent_id  uuid        REFERENCES categories (id) ON DELETE RESTRICT,
    slug       text        NOT NULL UNIQUE,
    name_uz    text        NOT NULL,
    name_ru    text        NOT NULL,
    level      smallint    NOT NULL DEFAULT 0,
    path       text        NOT NULL,
    sort_order smallint    NOT NULL DEFAULT 0,
    is_active  boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_categories_parent ON categories (parent_id, sort_order);
CREATE INDEX idx_categories_path   ON categories (path);

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
DROP TABLE categories;

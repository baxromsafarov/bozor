-- +goose Up
-- Определения атрибутов объявлений (по типу значения).
CREATE TABLE attributes (
    id            uuid PRIMARY KEY,
    slug          text        NOT NULL UNIQUE,
    name_uz       text        NOT NULL,
    name_ru       text        NOT NULL,
    type          text        NOT NULL CHECK (type IN ('enum', 'int', 'decimal', 'bool', 'string')),
    unit          text,       -- единица измерения (m², км, ...), опционально
    is_required   boolean     NOT NULL DEFAULT false,
    is_filterable boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Варианты значений для атрибутов типа enum.
CREATE TABLE attribute_options (
    id           uuid PRIMARY KEY,
    attribute_id uuid     NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    slug         text     NOT NULL,
    name_uz      text     NOT NULL,
    name_ru      text     NOT NULL,
    sort_order   smallint NOT NULL DEFAULT 0,
    UNIQUE (attribute_id, slug)
);

CREATE INDEX idx_attribute_options_attr ON attribute_options (attribute_id, sort_order);

-- Привязка атрибута к категории. Наследуется вниз по дереву (эффективные
-- атрибуты категории = её собственные + всех предков по материализованному path).
CREATE TABLE category_attributes (
    category_id  uuid     NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    attribute_id uuid     NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    sort_order   smallint NOT NULL DEFAULT 0,
    PRIMARY KEY (category_id, attribute_id)
);

CREATE INDEX idx_category_attributes_attr ON category_attributes (attribute_id);

-- +goose Down
DROP TABLE category_attributes;
DROP TABLE attribute_options;
DROP TABLE attributes;

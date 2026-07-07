-- +goose Up
-- Регионы Узбекистана (области/город/республика). id — стабильный код из сида.
CREATE TABLE regions (
    id         smallint    PRIMARY KEY,
    slug       text        NOT NULL UNIQUE,
    name_uz    text        NOT NULL,
    name_ru    text        NOT NULL,
    latitude   double precision,
    longitude  double precision,
    sort_order smallint    NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Города/районы, привязанные к региону.
CREATE TABLE cities (
    id         integer     PRIMARY KEY,
    region_id  smallint    NOT NULL REFERENCES regions (id) ON DELETE CASCADE,
    slug       text        NOT NULL UNIQUE,
    name_uz    text        NOT NULL,
    name_ru    text        NOT NULL,
    latitude   double precision,
    longitude  double precision,
    sort_order smallint    NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_cities_region ON cities (region_id, sort_order);

-- +goose Down
DROP TABLE cities;
DROP TABLE regions;

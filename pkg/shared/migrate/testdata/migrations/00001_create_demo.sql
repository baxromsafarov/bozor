-- +goose Up
CREATE TABLE demo_items (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE demo_items;

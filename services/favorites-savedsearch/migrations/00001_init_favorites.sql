-- +goose Up
-- Избранное пользователя: пара (user_id, ad_id). ad_id из Listing; кросс-БД FK
-- нет (db-per-service). Наличие объявления на записи не проверяется — при
-- удалении объявления запись убирает потребитель bozor.ad.deleted (Stage 5.2).
CREATE TABLE favorites (
    user_id    uuid        NOT NULL,
    ad_id      uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, ad_id)
);

-- Лента избранного пользователя (свежие сверху).
CREATE INDEX idx_favorites_user ON favorites (user_id, created_at DESC);
-- Очистка избранного при удалении объявления (bozor.ad.deleted).
CREATE INDEX idx_favorites_ad ON favorites (ad_id);

-- +goose Down
DROP TABLE favorites;

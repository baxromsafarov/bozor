-- +goose Up
-- Промо-состояние объявления (Stage 8.6, ADR-043). Listing — источник истины для
-- is_top/promotion_rank/promo_ends_at: Search-индекс (Typesense) перестраиваем из
-- Listing по fetch-current-state, поэтому промо-флаги обязаны жить в write-модели,
-- иначе полная переиндексация (--reindex) стёрла бы весь Топ. Проставляются
-- консьюмером bozor.promotion.activated, снимаются воркером истечения по promo_ends_at.
--   is_top          — объявление в топ-блоке выдачи Search.
--   promotion_rank  — вес в топ-блоке (unix-секунды окончания промо: позже истекает — выше).
--   promo_ends_at   — момент окончания продвижения TOP (NULL — промо нет).
ALTER TABLE ads
    ADD COLUMN is_top         boolean NOT NULL DEFAULT false,
    ADD COLUMN promotion_rank integer NOT NULL DEFAULT 0,
    ADD COLUMN promo_ends_at  timestamptz;

-- Воркер истечения TOP выбирает продвинутые объявления с истёкшим промо.
CREATE INDEX idx_ads_promo_expires ON ads (promo_ends_at) WHERE is_top;

-- +goose Down
DROP INDEX idx_ads_promo_expires;
ALTER TABLE ads
    DROP COLUMN promo_ends_at,
    DROP COLUMN promotion_rank,
    DROP COLUMN is_top;

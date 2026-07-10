-- +goose Up
-- Взаимодействие сроков услуги с жизненным циклом объявления (Stage 8.7, ADR-044).
--   suspended_at — момент приостановки услуги при истечении объявления (freeze
--                  оплаченного срока; при реактивации ends_at сдвигается вперёд на
--                  длительность простоя).
--   refunded_at  — момент возврата при снятии/удалении объявления (аудит; статус
--                  переходит в 'refunded').
ALTER TABLE ad_promotions
    ADD COLUMN suspended_at timestamptz,
    ADD COLUMN refunded_at  timestamptz;

-- +goose Down
ALTER TABLE ad_promotions
    DROP COLUMN refunded_at,
    DROP COLUMN suspended_at;

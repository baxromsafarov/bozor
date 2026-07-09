-- +goose Up
-- bump_runs — отметки исполненных дней авто-поднятия (Stage 8.5). Обеспечивает
-- идемпотентность воркера: каждый день расписания BUMP исполняется ровно один раз,
-- повторный тик (или рестарт процесса) не поднимает объявление повторно.
CREATE TABLE bump_runs (
    promotion_id uuid        NOT NULL REFERENCES ad_promotions (id) ON DELETE CASCADE,
    day_offset   integer     NOT NULL,
    executed_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (promotion_id, day_offset)
);

COMMENT ON TABLE bump_runs IS 'Исполненные дни авто-поднятия (идемпотентность воркера Stage 8.5).';

-- +goose Down
DROP TABLE bump_runs;

-- +goose Up
-- Ручные решения модератора (Stage 6.3): кто решил, комментарий/причина, когда.
-- Дополняет moderation_tasks; status пополняется значением edit_requested
-- (возврат на доработку — объявление в Listing становится rejected/editable).
ALTER TABLE moderation_tasks
    ADD COLUMN decided_by uuid,          -- модератор, принявший ручное решение
    ADD COLUMN comment    text,          -- причина reject / пояснение request-edit
    ADD COLUMN decided_at timestamptz;   -- когда принято ручное решение

-- +goose Down
ALTER TABLE moderation_tasks
    DROP COLUMN decided_at,
    DROP COLUMN comment,
    DROP COLUMN decided_by;

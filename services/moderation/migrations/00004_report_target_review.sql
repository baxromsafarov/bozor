-- +goose Up
-- Разрешаем жалобы на отзывы (Stage 9.1, ADR-045): target_type += 'review'.
-- Снятие отзыва модератором (takedown) публикует bozor.review.blocked.
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('ad', 'user', 'message', 'review'));

-- +goose Down
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('ad', 'user', 'message'));

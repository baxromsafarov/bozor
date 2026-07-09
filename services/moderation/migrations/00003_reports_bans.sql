-- +goose Up
-- Жалобы пользователей на объявления/пользователей/сообщения (Stage 6.4).
-- Публичная подача (POST /reports); очередь разбирает модератор.
CREATE TABLE reports (
    id          uuid PRIMARY KEY,
    reporter_id uuid        NOT NULL,
    target_type text        NOT NULL CHECK (target_type IN ('ad', 'user', 'message')),
    target_id   uuid        NOT NULL,
    reason      text        NOT NULL,
    status      text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolution  text,                    -- действие модератора + примечание
    resolved_by uuid,                    -- модератор, закрывший жалобу
    resolved_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Открытая очередь жалоб в порядке поступления.
CREATE INDEX idx_reports_status ON reports (status, created_at DESC);
-- Жалобы по объекту (сколько раз пожаловались на объявление/пользователя).
CREATE INDEX idx_reports_target ON reports (target_type, target_id);

-- Баны пользователей: временные (expires_at), постоянные и shadow (expires_at NULL).
CREATE TABLE bans (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL,
    type       text        NOT NULL CHECK (type IN ('temporary', 'permanent', 'shadow')),
    reason     text        NOT NULL DEFAULT '',
    expires_at timestamptz,             -- срок для temporary; NULL для permanent/shadow
    created_by uuid        NOT NULL,    -- модератор
    created_at timestamptz NOT NULL DEFAULT now()
);

-- История банов пользователя (последний активный — сверху).
CREATE INDEX idx_bans_user ON bans (user_id, created_at DESC);

-- +goose Down
DROP TABLE bans;
DROP TABLE reports;

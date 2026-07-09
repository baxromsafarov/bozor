-- +goose Up
-- Диалог покупатель↔продавец по объявлению (Stage 7.1). Уникален по тройке
-- (ad_id, buyer_id, seller_id): один диалог на пару участников по объявлению.
CREATE TABLE conversations (
    id              uuid PRIMARY KEY,
    ad_id           uuid        NOT NULL,
    buyer_id        uuid        NOT NULL,   -- инициатор (спрашивает по объявлению)
    seller_id       uuid        NOT NULL,   -- владелец объявления
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_message_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ad_id, buyer_id, seller_id)
);

-- Список диалогов пользователя (как покупателя и как продавца), свежие сверху.
CREATE INDEX idx_conversations_buyer ON conversations (buyer_id, last_message_at DESC);
CREATE INDEX idx_conversations_seller ON conversations (seller_id, last_message_at DESC);

-- Сообщения диалога. read_at — отметка о прочтении (счётчики непрочитанных — 7.3).
CREATE TABLE messages (
    id              uuid PRIMARY KEY,
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    sender_id       uuid        NOT NULL,
    body            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    read_at         timestamptz
);

-- История диалога в хронологическом порядке.
CREATE INDEX idx_messages_conversation ON messages (conversation_id, created_at);

-- Transactional outbox: публикация bozor.chat.message_sent → Notification
-- (уведомление адресата; в 7.2 доставка онлайн-получателю пойдёт по WebSocket).
CREATE TABLE outbox (
    id           uuid PRIMARY KEY,
    subject      text  NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE messages;
DROP TABLE conversations;

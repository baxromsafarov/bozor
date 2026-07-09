// Package repo содержит репозиторий Chat-сервиса (PostgreSQL через pgx):
// диалоги, сообщения, transactional outbox.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/chat/internal/domain"
)

// Repo — репозиторий чата.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

const convColumns = `id, ad_id, buyer_id, seller_id, created_at, last_message_at`

// EnsureConversation создаёт диалог либо возвращает существующий (уникальность по
// тройке ad_id+buyer_id+seller_id). ON CONFLICT DO UPDATE (no-op) позволяет
// вернуть уже существующую строку с её исходными id/created_at.
func (r *Repo) EnsureConversation(ctx context.Context, conv domain.Conversation) (domain.Conversation, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO conversations (id, ad_id, buyer_id, seller_id, created_at, last_message_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (ad_id, buyer_id, seller_id)
		DO UPDATE SET ad_id = EXCLUDED.ad_id
		RETURNING `+convColumns,
		conv.ID, conv.AdID, conv.BuyerID, conv.SellerID, conv.CreatedAt)
	got, err := scanConversation(row)
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("repo: создание диалога: %w", err)
	}
	return got, nil
}

// GetConversation возвращает диалог по id. found=false, если диалога нет.
func (r *Repo) GetConversation(ctx context.Context, id string) (domain.Conversation, bool, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+convColumns+` FROM conversations WHERE id = $1`, id)
	conv, err := scanConversation(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Conversation{}, false, nil
	case err != nil:
		return domain.Conversation{}, false, fmt.Errorf("repo: чтение диалога %s: %w", id, err)
	}
	return conv, true, nil
}

// ListConversations возвращает диалоги пользователя (как покупателя и как
// продавца), свежие сверху, с числом непрочитанных для запросившего (сообщения
// собеседника без read_at).
func (r *Repo) ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.ad_id, c.buyer_id, c.seller_id, c.created_at, c.last_message_at,
		       (SELECT count(*) FROM messages m
		        WHERE m.conversation_id = c.id AND m.sender_id <> $1 AND m.read_at IS NULL) AS unread
		FROM conversations c
		WHERE c.buyer_id = $1 OR c.seller_id = $1
		ORDER BY c.last_message_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: список диалогов: %w", err)
	}
	defer rows.Close()

	var out []domain.Conversation
	for rows.Next() {
		var c domain.Conversation
		if err := rows.Scan(&c.ID, &c.AdID, &c.BuyerID, &c.SellerID, &c.CreatedAt, &c.LastMessageAt, &c.UnreadCount); err != nil {
			return nil, fmt.Errorf("repo: скан диалога: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход диалогов: %w", err)
	}
	return out, nil
}

// ListMessages возвращает последние limit сообщений диалога в хронологическом
// порядке (старые → новые).
func (r *Repo) ListMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, sender_id, body, created_at, read_at
		FROM (
			SELECT id, conversation_id, sender_id, body, created_at, read_at
			FROM messages WHERE conversation_id = $1
			ORDER BY created_at DESC LIMIT $2
		) sub
		ORDER BY created_at ASC`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: список сообщений: %w", err)
	}
	defer rows.Close()

	var out []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Body, &m.CreatedAt, &m.ReadAt); err != nil {
			return nil, fmt.Errorf("repo: скан сообщения: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход сообщений: %w", err)
	}
	return out, nil
}

// InsertMessage сохраняет сообщение, двигает last_message_at диалога и — если ev
// задано — публикует bozor.chat.message_sent, всё одной транзакцией. ev=nil, когда
// получатель онлайн (доставка по WS, без Telegram-уведомления) — см. app.SendMessage.
func (r *Repo) InsertMessage(ctx context.Context, msg domain.Message, ev *events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, sender_id, body, created_at)
			VALUES ($1, $2, $3, $4, $5)`,
			msg.ID, msg.ConversationID, msg.SenderID, msg.Body, msg.CreatedAt)
		if err != nil {
			return fmt.Errorf("repo: вставка сообщения: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET last_message_at = $2 WHERE id = $1`,
			msg.ConversationID, msg.CreatedAt); err != nil {
			return fmt.Errorf("repo: обновление last_message_at: %w", err)
		}
		if ev == nil {
			return nil
		}
		return outbox.Enqueue(ctx, tx, *ev)
	})
}

// MarkRead помечает прочитанными сообщения собеседника в диалоге (sender_id ≠
// readerID, read_at IS NULL). Возвращает число помеченных.
func (r *Repo) MarkRead(ctx context.Context, conversationID, readerID string, at time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE messages SET read_at = $3
		WHERE conversation_id = $1 AND sender_id <> $2 AND read_at IS NULL`,
		conversationID, readerID, at)
	if err != nil {
		return 0, fmt.Errorf("repo: отметка прочтения: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanConversation читает строку диалога (поддерживает *pgxpool.Row и pgx.Rows).
func scanConversation(row pgx.Row) (domain.Conversation, error) {
	var c domain.Conversation
	err := row.Scan(&c.ID, &c.AdID, &c.BuyerID, &c.SellerID, &c.CreatedAt, &c.LastMessageAt)
	return c, err
}

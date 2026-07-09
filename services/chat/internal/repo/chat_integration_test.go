//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/migrate"

	"bozor/services/chat/internal/domain"
	"bozor/services/chat/internal/repo"
	"bozor/services/chat/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_chat"),
		tcpostgres.WithUsername("bozor"),
		tcpostgres.WithPassword("bozor"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	_, err = migrate.Up(ctx, dsn, migrations.FS)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func conv(t *testing.T, adID, buyer, seller string) domain.Conversation {
	t.Helper()
	now := time.Now().UTC()
	return domain.Conversation{ID: newID(t), AdID: adID, BuyerID: buyer, SellerID: seller,
		CreatedAt: now, LastMessageAt: now}
}

// TestEnsureConversation_Idempotent — повторный старт диалога по той же тройке
// (ad_id, buyer, seller) возвращает существующую строку, а не плодит дубликаты.
func TestEnsureConversation_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, buyer, seller := newID(t), newID(t), newID(t)
	first, err := r.EnsureConversation(ctx, conv(t, adID, buyer, seller))
	require.NoError(t, err)

	// Повторный старт с ДРУГИМ сгенерированным id — должен вернуть первую строку.
	second, err := r.EnsureConversation(ctx, conv(t, adID, buyer, seller))
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "тот же диалог, а не новый")

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM conversations`).Scan(&n))
	assert.Equal(t, 1, n, "дубликат не создан")
}

// chatEvent строит свежий конверт bozor.chat.message_sent (уникальный event_id).
func chatEvent(t *testing.T, recipient string) *events.Envelope {
	t.Helper()
	ev, err := events.New(events.SubjectChatMessageSent, "chat", map[string]string{"user_id": recipient})
	require.NoError(t, err)
	return &ev
}

// TestInsertMessage_UpdatesConversationAndOutbox — отправка сообщения вставляет
// строку, двигает last_message_at и (если ev задано) кладёт событие в outbox.
// ev=nil (получатель онлайн) — без события в outbox.
func TestInsertMessage_UpdatesConversationAndOutbox(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, buyer, seller := newID(t), newID(t), newID(t)
	c, err := r.EnsureConversation(ctx, conv(t, adID, buyer, seller))
	require.NoError(t, err)

	msgTime := time.Now().UTC().Add(time.Minute)
	msg := domain.Message{ID: newID(t), ConversationID: c.ID, SenderID: buyer, Body: "привет", CreatedAt: msgTime}
	require.NoError(t, r.InsertMessage(ctx, msg, chatEvent(t, seller)))

	var (
		body    string
		lastMsg time.Time
		outbox  int
	)
	require.NoError(t, pool.QueryRow(ctx, `SELECT body FROM messages WHERE id=$1`, msg.ID).Scan(&body))
	assert.Equal(t, "привет", body)
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_message_at FROM conversations WHERE id=$1`, c.ID).Scan(&lastMsg))
	assert.WithinDuration(t, msgTime, lastMsg, time.Second, "last_message_at сдвинут ко времени сообщения")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectChatMessageSent).Scan(&outbox))
	assert.Equal(t, 1, outbox, "событие в outbox")

	// Второе сообщение без события (получатель онлайн) — outbox не растёт.
	msg2 := domain.Message{ID: newID(t), ConversationID: c.ID, SenderID: buyer, Body: "ещё", CreatedAt: msgTime.Add(time.Minute)}
	require.NoError(t, r.InsertMessage(ctx, msg2, nil))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectChatMessageSent).Scan(&outbox))
	assert.Equal(t, 1, outbox, "онлайн-доставка не плодит событие")
}

// TestMarkRead_And_UnreadCount — непрочитанные считаются для собеседника, а
// MarkRead помечает только чужие сообщения и обнуляет счётчик.
func TestMarkRead_And_UnreadCount(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, buyer, seller := newID(t), newID(t), newID(t)
	c, err := r.EnsureConversation(ctx, conv(t, adID, buyer, seller))
	require.NoError(t, err)

	base := time.Now().UTC()
	// Покупатель написал 2 сообщения продавцу.
	for i := 0; i < 2; i++ {
		m := domain.Message{ID: newID(t), ConversationID: c.ID, SenderID: buyer, Body: "q", CreatedAt: base.Add(time.Duration(i) * time.Second)}
		require.NoError(t, r.InsertMessage(ctx, m, chatEvent(t, seller)))
	}

	// Для продавца непрочитанных = 2, для покупателя (свои же сообщения) = 0.
	sellerConvs, err := r.ListConversations(ctx, seller, 10)
	require.NoError(t, err)
	require.Len(t, sellerConvs, 1)
	assert.Equal(t, 2, sellerConvs[0].UnreadCount, "непрочитанные собеседника")
	buyerConvs, err := r.ListConversations(ctx, buyer, 10)
	require.NoError(t, err)
	assert.Equal(t, 0, buyerConvs[0].UnreadCount, "свои сообщения не непрочитаны")

	// Продавец читает: помечаются только сообщения покупателя.
	n, err := r.MarkRead(ctx, c.ID, seller, time.Now().UTC())
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	sellerConvs, err = r.ListConversations(ctx, seller, 10)
	require.NoError(t, err)
	assert.Equal(t, 0, sellerConvs[0].UnreadCount, "после прочтения — 0")

	// Повторный MarkRead — нечего помечать.
	n, err = r.MarkRead(ctx, c.ID, seller, time.Now().UTC())
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}

// TestListConversations_ByParticipant — диалоги видны и покупателю, и продавцу,
// свежие сверху.
func TestListConversations_ByParticipant(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	seller := newID(t)
	buyerA, buyerB := newID(t), newID(t)
	// Два диалога продавца с разными покупателями.
	cA, err := r.EnsureConversation(ctx, conv(t, newID(t), buyerA, seller))
	require.NoError(t, err)
	cB, err := r.EnsureConversation(ctx, conv(t, newID(t), buyerB, seller))
	require.NoError(t, err)

	// Сообщение в cB делает его свежее.
	require.NoError(t, r.InsertMessage(ctx,
		domain.Message{ID: newID(t), ConversationID: cB.ID, SenderID: buyerB, Body: "hi", CreatedAt: time.Now().UTC().Add(time.Hour)}, chatEvent(t, seller)))

	// Продавец видит оба, свежий (cB) — первым.
	sellerConvs, err := r.ListConversations(ctx, seller, 10)
	require.NoError(t, err)
	require.Len(t, sellerConvs, 2)
	assert.Equal(t, cB.ID, sellerConvs[0].ID, "свежий диалог сверху")

	// Покупатель A видит только свой диалог.
	buyerConvs, err := r.ListConversations(ctx, buyerA, 10)
	require.NoError(t, err)
	require.Len(t, buyerConvs, 1)
	assert.Equal(t, cA.ID, buyerConvs[0].ID)
}

// TestListMessages_Chronological — история возвращается старые→новые.
func TestListMessages_Chronological(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, buyer, seller := newID(t), newID(t), newID(t)
	c, err := r.EnsureConversation(ctx, conv(t, adID, buyer, seller))
	require.NoError(t, err)

	base := time.Now().UTC()
	for i, txt := range []string{"первое", "второе", "третье"} {
		m := domain.Message{ID: newID(t), ConversationID: c.ID, SenderID: buyer, Body: txt,
			CreatedAt: base.Add(time.Duration(i) * time.Minute)}
		require.NoError(t, r.InsertMessage(ctx, m, chatEvent(t, seller)))
	}

	msgs, err := r.ListMessages(ctx, c.ID, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, "первое", msgs[0].Body)
	assert.Equal(t, "третье", msgs[2].Body)
}

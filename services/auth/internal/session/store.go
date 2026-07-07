// Package session хранит одноразовые nonce'ы логина в Redis. Сценарий:
// клиент инициирует вход (Init) и получает deep-link; пользователь открывает
// его в Telegram (бот вызывает Link) и делится контактом (бот вызывает
// Confirm); клиент опрашивает статус (Get). Nonce одноразовый и живёт
// ограниченное время (TTL), после чего трактуется как expired.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// TTL — время жизни nonce'а логина (в пределах 2–5 мин по ROADMAP 1.3).
const TTL = 3 * time.Minute

// nonceBytes — длина случайного nonce в байтах (даёт 32 hex-символа).
const nonceBytes = 16

// Статусы логин-сессии.
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusExpired   = "expired"
)

// Доменные ошибки хранилища сессий.
var (
	// ErrNotFound — nonce отсутствует (истёк TTL либо неверен).
	ErrNotFound = errors.New("session: nonce не найден")
	// ErrNotPending — nonce есть, но уже не pending (подтверждён/использован).
	// Обеспечивает одноразовость: связать/подтвердить можно только pending.
	ErrNotPending = errors.New("session: nonce не в статусе pending")
)

// Session — состояние логина по nonce.
type Session struct {
	Status         string `json:"status"`
	UserID         string `json:"user_id,omitempty"`
	TelegramUserID int64  `json:"telegram_user_id,omitempty"`
}

// Store — Redis-хранилище nonce'ов логина.
type Store struct {
	rdb redis.Cmdable
	ttl time.Duration
}

// NewStore создаёт хранилище поверх клиента Redis.
func NewStore(rdb redis.Cmdable) *Store {
	return &Store{rdb: rdb, ttl: TTL}
}

func nonceKey(nonce string) string   { return "auth:nonce:" + nonce }
func loginKey(tgUserID int64) string { return fmt.Sprintf("auth:login:%d", tgUserID) }

// Init создаёт новый pending-nonce и возвращает его вместе с TTL.
func (s *Store) Init(ctx context.Context) (string, time.Duration, error) {
	buf := make([]byte, nonceBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, fmt.Errorf("session: генерация nonce: %w", err)
	}
	nonce := hex.EncodeToString(buf)

	payload, err := json.Marshal(Session{Status: StatusPending})
	if err != nil {
		return "", 0, fmt.Errorf("session: сериализация: %w", err)
	}
	// NX страхует от (астрономически маловероятной) коллизии nonce.
	ok, err := s.rdb.SetNX(ctx, nonceKey(nonce), payload, s.ttl).Result()
	if err != nil {
		return "", 0, fmt.Errorf("session: запись nonce: %w", err)
	}
	if !ok {
		return "", 0, errors.New("session: коллизия nonce")
	}
	return nonce, s.ttl, nil
}

// Link связывает pending-nonce с Telegram-пользователем, открывшим deep-link:
// сохраняет обратную привязку tgUserID → nonce, чтобы следующий контакт от
// этого пользователя подтвердил именно этот nonce. Связать можно только
// pending-nonce (иначе ErrNotFound/ErrNotPending) — так гарантируется
// одноразовость входа.
func (s *Store) Link(ctx context.Context, nonce string, tgUserID int64) error {
	sess, err := s.get(ctx, nonce)
	if err != nil {
		return err
	}
	if sess.Status != StatusPending {
		return ErrNotPending
	}
	if err := s.rdb.Set(ctx, loginKey(tgUserID), nonce, s.ttl).Err(); err != nil {
		return fmt.Errorf("session: привязка логина: %w", err)
	}
	return nil
}

// Confirm подтверждает логин после успешной регистрации контакта: находит
// nonce, привязанный к tgUserID, помечает его confirmed с userID и снимает
// привязку. Возвращает подтверждённый nonce; пустая строка без ошибки —
// у пользователя не было ожидающего входа (обычная регистрация без deep-link).
func (s *Store) Confirm(ctx context.Context, tgUserID int64, userID string) (string, error) {
	nonce, err := s.rdb.Get(ctx, loginKey(tgUserID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil // нет ожидающего входа — не ошибка
	}
	if err != nil {
		return "", fmt.Errorf("session: чтение привязки: %w", err)
	}

	sess, err := s.get(ctx, nonce)
	if errors.Is(err, ErrNotFound) {
		// nonce истёк, пока пользователь делился контактом — снимаем привязку.
		_ = s.rdb.Del(ctx, loginKey(tgUserID)).Err()
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	sess.Status = StatusConfirmed
	sess.UserID = userID
	sess.TelegramUserID = tgUserID
	payload, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("session: сериализация: %w", err)
	}
	// KeepTTL сохраняет исходный TTL: confirmed-nonce не должен жить вечно.
	if err := s.rdb.Set(ctx, nonceKey(nonce), payload, redis.KeepTTL).Err(); err != nil {
		return "", fmt.Errorf("session: подтверждение nonce: %w", err)
	}
	_ = s.rdb.Del(ctx, loginKey(tgUserID)).Err()
	return nonce, nil
}

// Get возвращает состояние логина по nonce. Отсутствие ключа (истёк/неверен)
// отдаётся как StatusExpired, а не ошибка — так удобно опрашивать клиенту.
func (s *Store) Get(ctx context.Context, nonce string) (Session, error) {
	sess, err := s.get(ctx, nonce)
	if errors.Is(err, ErrNotFound) {
		return Session{Status: StatusExpired}, nil
	}
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) get(ctx context.Context, nonce string) (Session, error) {
	raw, err := s.rdb.Get(ctx, nonceKey(nonce)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session: чтение nonce: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return Session{}, fmt.Errorf("session: разбор nonce: %w", err)
	}
	return sess, nil
}

// Package views реализует буфер счётчика просмотров объявлений в Redis:
// инкременты копятся в одном хэше (без write-hotspot на ads), а воркер
// периодически сливает агрегаты в PostgreSQL (Stage 3.5).
package views

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// bufferKey — Redis-хэш «ad_id → накопленные просмотры» до флеша в БД.
const bufferKey = "listing:views"

// drainScript атомарно снимает и обнуляет весь буфер: возвращает содержимое
// хэша и удаляет ключ одной операцией (EVAL однопоточен), поэтому инкременты,
// пришедшие после снятия, попадают в новый ключ и не теряются.
var drainScript = redis.NewScript(`
local data = redis.call('HGETALL', KEYS[1])
redis.call('DEL', KEYS[1])
return data
`)

// Counter — буфер просмотров поверх Redis.
type Counter struct {
	rdb redis.Cmdable
}

// NewCounter создаёт буфер поверх клиента Redis.
func NewCounter(rdb redis.Cmdable) *Counter {
	return &Counter{rdb: rdb}
}

// Incr увеличивает буферизованный счётчик просмотров объявления на единицу.
func (c *Counter) Incr(ctx context.Context, adID string) error {
	if err := c.rdb.HIncrBy(ctx, bufferKey, adID, 1).Err(); err != nil {
		return fmt.Errorf("views: инкремент %q: %w", adID, err)
	}
	return nil
}

// Buffered возвращает ещё не слитые в БД просмотры объявления (0, если нет).
func (c *Counter) Buffered(ctx context.Context, adID string) (int64, error) {
	n, err := c.rdb.HGet(ctx, bufferKey, adID).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("views: чтение буфера %q: %w", adID, err)
	}
	return n, nil
}

// Drain атомарно снимает и обнуляет весь буфер, возвращая карту ad_id → просмотры.
func (c *Counter) Drain(ctx context.Context) (map[string]int64, error) {
	res, err := drainScript.Run(ctx, c.rdb, []string{bufferKey}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("views: снятие буфера: %w", err)
	}
	flat, ok := res.([]any)
	if !ok {
		return nil, fmt.Errorf("views: неожиданный ответ снятия буфера: %T", res)
	}
	out := make(map[string]int64, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		adID, _ := flat[i].(string)
		raw, _ := flat[i+1].(string)
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("views: разбор счётчика %q: %w", adID, err)
		}
		if adID != "" && n != 0 {
			out[adID] = n
		}
	}
	return out, nil
}

// Restore возвращает счётчики в буфер (best-effort откат, если флеш в БД не удался).
func (c *Counter) Restore(ctx context.Context, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	pipe := c.rdb.Pipeline()
	for adID, n := range counts {
		pipe.HIncrBy(ctx, bufferKey, adID, n)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("views: возврат буфера: %w", err)
	}
	return nil
}

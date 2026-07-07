package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Ключи кеша эффективных атрибутов. Инвалидация — через счётчик поколения
// (INCR): любая правка атрибутов/привязок делает старые записи недостижимыми,
// они истекают по TTL. Так сбрасывается весь кеш одним запросом без SCAN.
const (
	attrGenKey    = "catalog:attrs:gen"
	attrKeyPrefix = "catalog:attrs:"
)

// AttrCache кеширует готовый JSON-ответ эффективных атрибутов категории.
type AttrCache struct {
	rdb redis.Cmdable
	ttl time.Duration
}

// NewAttrCache создаёт кеш поверх клиента Redis.
func NewAttrCache(rdb redis.Cmdable, ttl time.Duration) *AttrCache {
	return &AttrCache{rdb: rdb, ttl: ttl}
}

// Get возвращает кешированный ответ для категории или nil при промахе.
func (c *AttrCache) Get(ctx context.Context, categoryID string) ([]byte, error) {
	gen, err := c.gen(ctx)
	if err != nil {
		return nil, err
	}
	data, err := c.rdb.Get(ctx, c.key(gen, categoryID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: чтение атрибутов: %w", err)
	}
	return data, nil
}

// Set кладёт ответ категории в кеш текущего поколения с TTL.
func (c *AttrCache) Set(ctx context.Context, categoryID string, data []byte) error {
	gen, err := c.gen(ctx)
	if err != nil {
		return err
	}
	if err := c.rdb.Set(ctx, c.key(gen, categoryID), data, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache: запись атрибутов: %w", err)
	}
	return nil
}

// Invalidate увеличивает поколение — весь кеш эффективных атрибутов сбрасывается.
func (c *AttrCache) Invalidate(ctx context.Context) error {
	if err := c.rdb.Incr(ctx, attrGenKey).Err(); err != nil {
		return fmt.Errorf("cache: инвалидация атрибутов: %w", err)
	}
	return nil
}

// gen читает текущее поколение (0, если ещё не задано).
func (c *AttrCache) gen(ctx context.Context) (int64, error) {
	n, err := c.rdb.Get(ctx, attrGenKey).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cache: чтение поколения атрибутов: %w", err)
	}
	return n, nil
}

// key собирает ключ записи для поколения и категории.
func (c *AttrCache) key(gen int64, categoryID string) string {
	return attrKeyPrefix + strconv.FormatInt(gen, 10) + ":" + categoryID
}

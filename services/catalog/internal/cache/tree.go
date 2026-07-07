// Package cache — Redis-кеш сериализованного дерева категорий.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// treeKey — ключ кеша дерева категорий.
const treeKey = "catalog:tree"

// TreeCache кеширует готовый JSON-ответ дерева категорий в Redis.
type TreeCache struct {
	rdb redis.Cmdable
	ttl time.Duration
}

// NewTreeCache создаёт кеш поверх клиента Redis.
func NewTreeCache(rdb redis.Cmdable, ttl time.Duration) *TreeCache {
	return &TreeCache{rdb: rdb, ttl: ttl}
}

// Get возвращает кешированные байты ответа или nil при промахе.
func (c *TreeCache) Get(ctx context.Context) ([]byte, error) {
	data, err := c.rdb.Get(ctx, treeKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: чтение дерева: %w", err)
	}
	return data, nil
}

// Set кладёт байты ответа в кеш с TTL.
func (c *TreeCache) Set(ctx context.Context, data []byte) error {
	if err := c.rdb.Set(ctx, treeKey, data, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache: запись дерева: %w", err)
	}
	return nil
}

// Invalidate удаляет кеш дерева (после любой записи в каталог).
func (c *TreeCache) Invalidate(ctx context.Context) error {
	if err := c.rdb.Del(ctx, treeKey).Err(); err != nil {
		return fmt.Errorf("cache: инвалидация дерева: %w", err)
	}
	return nil
}

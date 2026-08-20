package demo

import (
	"context"
	"sync"
	"time"
)

// MemoryCache 是示例用的最小 CacheProvider，不作为生产实现。
type MemoryCache struct {
	mu    sync.Mutex
	store map[string][]byte
}

// NewMemoryCache 创建空的进程内缓存。
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{store: map[string][]byte{}}
}

// Get 按 key 读取缓存。
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.store[key]
	return value, ok
}

// Set 写入缓存；示例忽略 TTL。
func (c *MemoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = value
}

// Delete 删除缓存键，供 middleware.DeleteCache 使用。
func (c *MemoryCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, key)
	return nil
}

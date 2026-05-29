package middleware

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fantasticbin/QueryBuilder/core"
)

// MiddlewareFunc 通用中间件函数签名。
// 与根包 Middleware[R] 签名兼容，builder 参数满足 core.QuerierMeta 接口。
type MiddlewareFunc[R any] func(
	ctx context.Context,
	builder core.QuerierMeta,
	next func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error)

// CacheProvider 缓存提供者接口
// 用户可自定义缓存后端实现（如 Redis、内存缓存等）
type CacheProvider interface {
	// Get 根据 key 获取缓存数据，返回缓存的字节数据和是否命中
	Get(ctx context.Context, key string) ([]byte, bool)
	// Set 设置缓存数据，key 为缓存键，value 为缓存的字节数据，ttl 为过期时间
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
}

// cacheResult 缓存结果结构体，用于序列化/反序列化查询结果
type cacheResult[R any] struct {
	List  []*R  `json:"list"`
	Total int64 `json:"total"`
}

// CacheMiddlewareWithKeyBuilder 使用 CacheKeyBuilder 构建缓存键。
// 中间件内部通过 builder.GetQueryMeta() 获取查询元信息，传递给 keyBuilder.Build
func CacheMiddlewareWithKeyBuilder[R any](cache CacheProvider, ttl time.Duration, keyBuilder CacheKeyBuilder) MiddlewareFunc[R] {
	if keyBuilder == nil {
		keyBuilder = DefaultCacheKeyBuilder{Prefix: "default"}
	}
	return CacheMiddleware[R](cache, ttl, func(ctx context.Context, b core.QuerierMeta) string {
		return keyBuilder.Build(ctx, b.GetQueryMeta())
	})
}

// CacheMiddleware 创建查询结果缓存中间件
// 该中间件会在查询前检查缓存，命中则直接返回缓存结果，未命中则执行查询并写入缓存
// 参数:
//
//	cache - 缓存提供者实例，实现 CacheProvider 接口
//	ttl   - 缓存过期时间
//	keyFn - 缓存 key 生成函数，接收 ctx 和 core.QuerierMeta 参数
//
// 返回:
//
//	MiddlewareFunc[R] - 可直接通过 Use 方法添加到构建器的中间件
func CacheMiddleware[R any](cache CacheProvider, ttl time.Duration, keyFn func(ctx context.Context, b core.QuerierMeta) string) MiddlewareFunc[R] {
	return func(ctx context.Context, builder core.QuerierMeta, next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
		key := keyFn(ctx, builder)

		// 尝试从缓存获取
		if data, ok := cache.Get(ctx, key); ok {
			var result cacheResult[R]
			if err := json.Unmarshal(data, &result); err == nil {
				return result.List, result.Total, nil
			}
		}

		// 缓存未命中，执行实际查询
		list, total, err := next(ctx)
		if err != nil {
			return list, total, err
		}

		// 将查询结果写入缓存
		result := cacheResult[R]{
			List:  list,
			Total: total,
		}
		if data, marshalErr := json.Marshal(result); marshalErr == nil {
			cache.Set(ctx, key, data, ttl)
		}

		return list, total, nil
	}
}

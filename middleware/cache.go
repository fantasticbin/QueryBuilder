package middleware

import (
	"context"
	"encoding/json"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
)

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
	Kind             core.ResultKind `json:"kind"`
	Items            []*R            `json:"items"`
	Total            int64           `json:"total"`
	HasMore          bool            `json:"has_more"`
	NextCursorValues []any           `json:"next_cursor_values"`
}

// toResult 将 cacheResult 转换为 core.Result[R]，根据 Kind 字段区分 CursorPageResult 和 ListResult
func (r cacheResult[R]) toResult() core.Result[R] {
	if r.Kind == core.ResultKindList {
		return &core.ListResult[R]{
			Items: r.Items,
			Total: r.Total,
		}
	}
	return &core.CursorPageResult[R]{
		Items:            r.Items,
		Total:            r.Total,
		HasMore:          r.HasMore,
		NextCursorValues: r.NextCursorValues,
	}
}

// cacheResultFromResult 从 core.Result[R] 构建 cacheResult[R]，提取公共字段并区分分页查询结果
func cacheResultFromResult[R any](result core.Result[R]) cacheResult[R] {
	if result == nil {
		return cacheResult[R]{Kind: core.ResultKindList}
	}
	return cacheResult[R]{
		Kind:             result.GetResultKind(),
		Items:            result.GetItems(),
		Total:            result.GetTotal(),
		HasMore:          result.GetHasMore(),
		NextCursorValues: result.GetNextCursorValues(),
	}
}

// CacheMiddlewareWithKeyBuilder 使用 CacheKeyBuilder 构建缓存键。
// 中间件内部通过 builder.GetQueryMeta() 获取查询元信息，传递给 keyBuilder.Build
func CacheMiddlewareWithKeyBuilder[R any](cache CacheProvider, ttl time.Duration, keyBuilder CacheKeyBuilder) builder.Middleware[R] {
	if keyBuilder == nil {
		keyBuilder = DefaultCacheKeyBuilder{Prefix: "default"}
	}
	return CacheMiddleware[R](cache, ttl, func(ctx context.Context, b builder.Querier[R]) string {
		return keyBuilder.Build(ctx, b.GetQueryMeta())
	})
}

// CacheMiddleware 创建查询结果缓存中间件
// 该中间件会在查询前检查缓存，命中则直接返回缓存结果，未命中则执行查询并写入缓存
// 参数:
//
//	cache - 缓存提供者实例，实现 CacheProvider 接口
//	ttl   - 缓存过期时间
//	keyFn - 缓存 key 生成函数，接收 ctx 和 builder.Querier[R] 参数（可通过 GetQueryMeta() 获取元信息）
//
// 返回:
//
//	builder.Middleware[R] - 可直接通过 Use 方法添加到构建器的中间件
func CacheMiddleware[R any](cache CacheProvider, ttl time.Duration, keyFn func(ctx context.Context, b builder.Querier[R]) string) builder.Middleware[R] {
	return func(ctx context.Context, b builder.Querier[R], next func(context.Context) (core.Result[R], error)) (core.Result[R], error) {
		if b.GetQueryMeta().IsPITQuery {
			return next(ctx)
		}

		key := keyFn(ctx, b)

		// 尝试从缓存获取
		if data, ok := cache.Get(ctx, key); ok {
			var result cacheResult[R]
			if err := json.Unmarshal(data, &result); err == nil {
				return result.toResult(), nil
			}
		}

		// 缓存未命中，执行实际查询
		result, err := next(ctx)
		if err != nil {
			return result, err
		}

		// 将查询结果写入缓存
		cacheValue := cacheResultFromResult(result)
		if data, marshalErr := json.Marshal(cacheValue); marshalErr == nil {
			cache.Set(ctx, key, data, ttl)
		}

		return result, nil
	}
}

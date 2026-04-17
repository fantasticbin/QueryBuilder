package builder

import (
	"context"
	"encoding/json"
	"time"
)

// Middleware 查询中间件类型定义
// 参数:
//
//	ctx: 上下文
//	builder: 查询构建器实例（Querier[R] 接口类型，提供基本的类型安全）
//	next: 下一个中间件或最终查询处理器
//
// 返回:
//
//	[]*R: 查询结果列表
//	int64: 总数
//	error: 错误信息
type Middleware[R any] func(
	ctx context.Context,
	builder Querier[R],
	next func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error)

// BeforeQueryHook 查询前置钩子函数类型
// 参数:
//
//	ctx: 上下文
//
// 返回:
//
//	context.Context: 可修改后的上下文
type BeforeQueryHook func(ctx context.Context) context.Context

// AfterQueryHook 查询后置钩子函数类型
// 泛型参数:
//
//	R: 查询结果的实体类型
//
// 参数:
//
//	ctx: 上下文
//	list: 查询结果列表
//	total: 总数
//	err: 错误信息
type AfterQueryHook[R any] func(ctx context.Context, list []*R, total int64, err error)

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

// CacheMiddleware 创建查询结果缓存中间件
// 该中间件会在查询前检查缓存，命中则直接返回缓存结果，未命中则执行查询并写入缓存
// 参数:
//
//	cache - 缓存提供者实例，实现 CacheProvider 接口
//	ttl   - 缓存过期时间
//	keyFn - 缓存 key 生成函数，由用户根据查询条件自定义生成唯一的缓存键
//
// 返回:
//
//	Middleware[R] - 可直接通过 Use 方法添加到构建器的中间件
func CacheMiddleware[R any](cache CacheProvider, ttl time.Duration, keyFn func(ctx context.Context) string) Middleware[R] {
	return func(ctx context.Context, builder Querier[R], next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
		key := keyFn(ctx)

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
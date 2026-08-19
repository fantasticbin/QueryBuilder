package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	"golang.org/x/sync/singleflight"
)

// AggregateCacheKeyBuilder 定义聚合缓存键构建接口
type AggregateCacheKeyBuilder interface {
	Build(context.Context, queryagg.Meta) string
}

// AggregateDefaultCacheKeyBuilder 用于构建确定性的聚合缓存键
type AggregateDefaultCacheKeyBuilder struct {
	Prefix        string
	Hints         CacheKeyHints
	HintsProvider func(context.Context) CacheKeyHints
}

// Build 返回聚合查询稳定的 SHA-256 缓存键
func (b AggregateDefaultCacheKeyBuilder) Build(ctx context.Context, meta queryagg.Meta) string {
	hints := resolveCacheKeyHints(ctx, b.Hints, b.HintsProvider)
	payload := map[string]any{
		"prefix":     b.Prefix,
		"datasource": meta.DataSource.String(),
		"spec":       meta.Spec,
		"pagination": map[string]any{
			"need_total":  meta.NeedTotal,
			"total_limit": meta.TotalLimit,
		},
	}
	appendCacheKeyHints(payload, hints)

	canonical := canonicalCachePayload(payload)
	hash := sha256.Sum256([]byte(canonical))
	return "qb:agg:cache:" + hex.EncodeToString(hash[:])
}

// aggregateCacheResult 是聚合缓存写入缓存后端的稳定序列化结构
type aggregateCacheResult[A any] struct {
	Rows  []*A  `json:"rows"`
	Total int64 `json:"total"`
}

// AggregateCacheMiddlewareWithKeyBuilder 使用指定缓存键构建器创建聚合缓存中间件
// 参数:
//
//	cache      - 缓存提供者实例，实现 CacheProvider 接口
//	ttl        - 缓存过期时间
//	keyBuilder - 缓存键构建器，基于 queryagg.Meta 生成确定性的缓存键
func AggregateCacheMiddlewareWithKeyBuilder[A any](
	cache CacheProvider,
	ttl time.Duration,
	keyBuilder AggregateCacheKeyBuilder,
) queryagg.Middleware[A] {
	if keyBuilder == nil {
		keyBuilder = AggregateDefaultCacheKeyBuilder{Prefix: "default"}
	}
	return AggregateCacheMiddleware[A](cache, ttl, func(ctx context.Context, querier queryagg.Querier[A]) string {
		return keyBuilder.Build(ctx, querier.Meta())
	})
}

// AggregateCacheMiddleware 创建聚合结果缓存中间件
// 命中缓存时直接返回缓存结果；未命中则执行查询并写入缓存。
// 同 key 的并发查询由 singleflight 合并为一次执行；缓存读取或反序列化失败均视为未命中，不阻断查询。
func AggregateCacheMiddleware[A any](
	cache CacheProvider,
	ttl time.Duration,
	key func(context.Context, queryagg.Querier[A]) string,
) queryagg.Middleware[A] {
	group := new(singleflight.Group)
	return func(
		ctx context.Context,
		querier queryagg.Querier[A],
		next queryagg.Handler[A],
	) (*queryagg.Result[A], error) {
		if cache == nil || key == nil {
			return next(ctx)
		}

		cacheKey := key(ctx, querier)
		value, err, _ := group.Do(cacheKey, func() (any, error) {
			if data, hit, lookupErr := cacheLookup(ctx, cache, cacheKey); lookupErr == nil && hit {
				var cached aggregateCacheResult[A]
				if err := json.Unmarshal(data, &cached); err == nil {
					if cached.Rows == nil {
						cached.Rows = make([]*A, 0)
					}
					return &queryagg.Result[A]{Rows: cached.Rows, Total: cached.Total}, nil
				}
			}

			result, err := next(ctx)
			if err != nil || result == nil {
				return result, err
			}
			encoded, marshalErr := json.Marshal(aggregateCacheResult[A]{Rows: result.Rows, Total: result.Total})
			if marshalErr == nil {
				_ = cacheStore(ctx, cache, cacheKey, encoded, ttl)
			}
			return result, nil
		})
		if value == nil {
			return nil, err
		}
		return value.(*queryagg.Result[A]), err
	}
}

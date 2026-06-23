// Package agg 提供聚合查询专用的缓存与可观测中间件
package agg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	querymiddleware "github.com/fantasticbin/QueryBuilder/v2/middleware"
)

// CacheProvider 是列表查询与聚合查询中间件共用的缓存存储接口
type CacheProvider = querymiddleware.CacheProvider

// CacheKeyHints 用后端过滤条件和业务维度补充聚合缓存键
type CacheKeyHints = querymiddleware.CacheKeyHints

// CacheKeyBuilder 定义聚合缓存键构建接口
type CacheKeyBuilder interface {
	Build(context.Context, queryagg.Meta) string
}

// DefaultCacheKeyBuilder 用于构建确定性的聚合缓存键
type DefaultCacheKeyBuilder struct {
	Prefix        string
	Hints         CacheKeyHints
	HintsProvider func(context.Context) CacheKeyHints
}

// Build 返回聚合查询稳定的 SHA-256 缓存键
func (b DefaultCacheKeyBuilder) Build(ctx context.Context, meta queryagg.Meta) string {
	hints := b.Hints
	hasStaticHints := hints.Filter != nil || hints.Sort != nil || len(hints.Extra) > 0
	if !hasStaticHints && b.HintsProvider != nil {
		hints = b.HintsProvider(ctx)
	}

	payload := map[string]any{
		"prefix":     b.Prefix,
		"datasource": meta.DataSource,
		"spec":       meta.Spec,
	}
	if hints.Filter != nil {
		payload["filter"] = hints.Filter
	}
	if hints.Sort != nil {
		payload["sort"] = hints.Sort
	}
	if len(hints.Extra) > 0 {
		payload["extra"] = hints.Extra
	}

	encoded, err := json.Marshal(normalizeCacheValue(payload))
	if err != nil {
		encoded = []byte(fmt.Sprintf("fallback:%#v", payload))
	}
	hash := sha256.Sum256(encoded)
	return "qb:agg:cache:" + hex.EncodeToString(hash[:])
}

// cacheResult 是聚合缓存的序列化结构
type cacheResult[A any] struct {
	Rows []*A `json:"rows"`
}

// CacheWithKeyBuilder 使用指定缓存键构建器创建聚合缓存中间件
func CacheWithKeyBuilder[A any](
	cache CacheProvider,
	ttl time.Duration,
	keyBuilder CacheKeyBuilder,
) queryagg.Middleware[A] {
	if keyBuilder == nil {
		keyBuilder = DefaultCacheKeyBuilder{Prefix: "default"}
	}
	return Cache[A](cache, ttl, func(ctx context.Context, querier queryagg.Querier[A]) string {
		return keyBuilder.Build(ctx, querier.Meta())
	})
}

// Cache 创建聚合结果缓存中间件
func Cache[A any](
	cache CacheProvider,
	ttl time.Duration,
	key func(context.Context, queryagg.Querier[A]) string,
) queryagg.Middleware[A] {
	return func(
		ctx context.Context,
		querier queryagg.Querier[A],
		next queryagg.Handler[A],
	) (*queryagg.Result[A], error) {
		if cache == nil || key == nil {
			return next(ctx)
		}

		cacheKey := key(ctx, querier)
		if data, ok := cache.Get(ctx, cacheKey); ok {
			var cached cacheResult[A]
			if err := json.Unmarshal(data, &cached); err == nil {
				if cached.Rows == nil {
					cached.Rows = make([]*A, 0)
				}
				return &queryagg.Result[A]{Rows: cached.Rows}, nil
			}
		}

		result, err := next(ctx)
		if err != nil || result == nil {
			return result, err
		}
		encoded, marshalErr := json.Marshal(cacheResult[A]{Rows: result.Rows})
		if marshalErr == nil {
			cache.Set(ctx, cacheKey, encoded, ttl)
		}
		return result, nil
	}
}

// normalizeCacheValue 将缓存键维度转换为可稳定序列化的值
func normalizeCacheValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized[key] = normalizeCacheValue(item)
		}
		return normalized
	case []any:
		normalized := make([]any, len(typed))
		for i, item := range typed {
			normalized[i] = normalizeCacheValue(item)
		}
		return normalized
	default:
		if _, err := json.Marshal(typed); err != nil {
			return fmt.Sprintf("%T:%v", typed, typed)
		}
		return typed
	}
}

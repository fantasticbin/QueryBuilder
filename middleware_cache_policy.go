package builder

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CacheKeyBuilder 定义缓存键构建接口，业务方可覆写默认实现。
type CacheKeyBuilder interface {
	Build(ctx context.Context) string
}

// cacheKeyHintsKey 是 cache key hints 在 context 中的 key。
type cacheKeyHintsKey struct{}

// CacheKeyHints 用于补充默认缓存键维度（如 filter/sort）。
// 如需多租户隔离，建议通过 Extra 传入 tenant_id 等稳定字段。
type CacheKeyHints struct {
	Filter     any
	Sort       any
	Pagination map[string]any
	Extra      map[string]any
}

// WithCacheKeyHints 将缓存键补充维度写入 context。
func WithCacheKeyHints(ctx context.Context, hints CacheKeyHints) context.Context {
	return context.WithValue(ctx, cacheKeyHintsKey{}, hints)
}

func cacheKeyHintsFromContext(ctx context.Context) (CacheKeyHints, bool) {
	h, ok := ctx.Value(cacheKeyHintsKey{}).(CacheKeyHints)
	return h, ok
}

// DefaultCacheKeyBuilder 为缓存中间件提供开箱即用的默认 key 方案。
type DefaultCacheKeyBuilder struct {
	Prefix string
}

func (b DefaultCacheKeyBuilder) Build(ctx context.Context) string {
	meta := QueryMetaFromContext(ctx)
	payload := map[string]any{"prefix": b.Prefix}
	if meta != nil {
		payload["datasource"] = meta.DataSource
		payload["fields"] = append([]string(nil), meta.Fields...)
		payload["pagination"] = map[string]any{
			"start":           meta.Start,
			"limit":           meta.Limit,
			"needTotal":       meta.NeedTotal,
			"needPagination":  meta.NeedPagination,
			"isCursorQuery":   meta.IsCursorQuery,
			"cursorFields":    append([]string(nil), meta.CursorFields...),
		}
	}
	if hints, ok := cacheKeyHintsFromContext(ctx); ok {
		if hints.Filter != nil {
			payload["filter"] = hints.Filter
		}
		if hints.Sort != nil {
			payload["sort"] = hints.Sort
		}
		if len(hints.Pagination) > 0 {
			payload["pagination_hint"] = hints.Pagination
		}
		if len(hints.Extra) > 0 {
			payload["extra"] = hints.Extra
		}
	}

	canonical := canonicalJSON(payload)
	h := sha1.Sum([]byte(canonical))
	return fmt.Sprintf("qb:cache:%s", hex.EncodeToString(h[:]))
}

func canonicalJSON(v any) string {
	n := normalizeValue(v)
	buf, _ := json.Marshal(n)
	return string(buf)
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(x))
		for _, k := range keys {
			ordered[k] = normalizeValue(x[k])
		}
		return ordered
	case []any:
		res := make([]any, len(x))
		for i := range x {
			res[i] = normalizeValue(x[i])
		}
		return res
	default:
		return v
	}
}

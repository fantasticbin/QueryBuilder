package builder

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CacheKeyBuilder 定义缓存键构建接口，业务方可覆写默认实现。
type CacheKeyBuilder interface {
	Build(ctx context.Context) string
}

// cacheKeyHintsKey 是 cache key hints 在 context 中的 key。
type cacheKeyHintsKey struct{}

// CacheKeyHints 用于补充默认缓存键维度（如 filter/sort）。
// 如需多租户隔离，建议通过 Extra 传入 tenant_id 等稳定字段。
// 注意：Filter/Sort 建议传入可稳定序列化的值（map/struct/切片/标量）；
// 若传入函数、channel 等不可 JSON 序列化值，会自动降级为字符串表示，避免 key 空串碰撞。
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
// Prefix 建议设置为业务资源名（如 "users"、"orders"），避免不同查询场景共享 key 空间。
type DefaultCacheKeyBuilder struct {
	Prefix string
	// OptionalHintsProvider 在 context 未显式注入 hints 时调用，用于减少调用方遗漏。
	OptionalHintsProvider func(context.Context) CacheKeyHints
}

func (b DefaultCacheKeyBuilder) Build(ctx context.Context) string {
	meta := QueryMetaFromContext(ctx)
	payload := map[string]any{"prefix": b.Prefix}
	if meta != nil {
		payload["datasource"] = meta.DataSource
		payload["fields"] = append([]string(nil), meta.Fields...)
		payload["pagination"] = map[string]any{
			"start":          meta.Start,
			"limit":          meta.Limit,
			"needTotal":      meta.NeedTotal,
			"needPagination": meta.NeedPagination,
			"isCursorQuery":  meta.IsCursorQuery,
			"cursorFields":   append([]string(nil), meta.CursorFields...),
		}
	}

	hints, ok := cacheKeyHintsFromContext(ctx)
	if !ok && b.OptionalHintsProvider != nil {
		hints = b.OptionalHintsProvider(ctx)
		ok = true
	}
	if ok {
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

	canonical, err := canonicalJSON(payload)
	if err != nil {
		// 兜底使用 fmt 格式，确保 key 不为空且低碰撞风险。
		canonical = fmt.Sprintf("fallback:%#v", normalizeValue(payload))
	}
	h := sha1.Sum([]byte(canonical))
	return fmt.Sprintf("qb:cache:%s", hex.EncodeToString(h[:]))
}

func canonicalJSON(v any) (string, error) {
	n := normalizeValue(v)
	buf, err := json.Marshal(n)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// normalizeValue 仅做可序列化防御，不再手工排序 map key（encoding/json 已稳定排序）。
func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		n := make(map[string]any, len(x))
		for k, val := range x {
			n[k] = normalizeValue(val)
		}
		return n
	case []any:
		res := make([]any, len(x))
		for i := range x {
			res[i] = normalizeValue(x[i])
		}
		return res
	case []string:
		res := make([]any, len(x))
		for i := range x {
			res[i] = x[i]
		}
		return res
	default:
		if _, err := json.Marshal(x); err != nil {
			return fmt.Sprintf("%T:%v", x, x)
		}
		return x
	}
}

package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// CacheKeyBuilder 定义缓存键构建接口，业务方可覆写默认实现
type CacheKeyBuilder interface {
	Build(ctx context.Context, meta core.QueryMeta) string
}

// CacheKeyHints 用于补充默认缓存键维度（如 filter/sort）
// 如需多租户隔离，建议通过 Extra 传入 tenant_id 等稳定字段
// 注意：Filter/Sort 建议传入可稳定序列化的值（map/struct/切片/标量）；
// 若传入函数、channel 等不可 JSON 序列化值，会自动降级为字符串表示，避免 key 空串碰撞
type CacheKeyHints struct {
	Filter any
	Sort   any
	Extra  map[string]any
}

// DefaultCacheKeyBuilder 为缓存中间件提供开箱即用的默认 key 方案
// Prefix 建议设置为业务资源名（如 "users"、"orders"），避免不同查询场景共享 key 空间
// Hints 为缓存键补充维度，由调用方在创建 DefaultCacheKeyBuilder 时注入，
// 对于 Clone 并发场景，每个 Clone 实例各自 Use 携带不同 Hints 的缓存中间件即可
type DefaultCacheKeyBuilder struct {
	Prefix string
	// Hints 缓存键补充维度（filter/sort/extra），由调用方在构建时注入
	Hints CacheKeyHints
	// HintsProvider 在 hints 为空时调用，用于减少调用方遗漏
	HintsProvider func(context.Context) CacheKeyHints
}

// Build 根据查询元信息和 Hints 构建确定性、抗碰撞的缓存键
// 内部将 prefix、datasource、fields、pagination 及 hints（filter/sort/extra）组装为规范化 JSON，
// 再取 SHA-256 摘要生成最终 key，格式为 "qb:cache:<hex>"
func (b DefaultCacheKeyBuilder) Build(ctx context.Context, meta core.QueryMeta) string {
	payload := map[string]any{"prefix": b.Prefix}
	payload["datasource"] = meta.DataSource.String()
	payload["fields"] = append([]string(nil), meta.Fields...)
	payload["pagination"] = map[string]any{
		"start":          meta.Start,
		"limit":          meta.Limit,
		"needTotal":      meta.NeedTotal,
		"totalLimit":     meta.TotalLimit,
		"needPagination": meta.NeedPagination,
		"isCursorQuery":  meta.IsCursorQuery,
		"isPITQuery":     meta.IsPITQuery,
		"cursorFields":   append([]string(nil), meta.CursorFields...),
		"cursorValues":   append([]any(nil), meta.CursorValues...),
	}

	hints := resolveCacheKeyHints(ctx, b.Hints, b.HintsProvider)
	appendCacheKeyHints(payload, hints)

	canonical := canonicalCachePayload(payload)
	h := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("qb:cache:%s", hex.EncodeToString(h[:]))
}

// resolveCacheKeyHints 在静态 hints 为空时延迟调用 provider
func resolveCacheKeyHints(
	ctx context.Context,
	hints CacheKeyHints,
	provider func(context.Context) CacheKeyHints,
) CacheKeyHints {
	if !hasCacheKeyHints(hints) && provider != nil {
		return provider(ctx)
	}
	return hints
}

// hasCacheKeyHints 判断调用方是否显式提供了任一缓存键补充维度
func hasCacheKeyHints(hints CacheKeyHints) bool {
	return hints.Filter != nil || hints.Sort != nil || len(hints.Extra) > 0
}

// appendCacheKeyHints 将 filter、sort、extra 注入缓存键 payload
func appendCacheKeyHints(payload map[string]any, hints CacheKeyHints) {
	if hints.Filter != nil {
		payload["filter"] = hints.Filter
	}
	if hints.Sort != nil {
		payload["sort"] = hints.Sort
	}
	if len(hints.Extra) > 0 {
		payload["extra"] = hints.Extra
	}
}

// canonicalCachePayload 将 payload 转换为稳定字符串，序列化失败时使用非空 fallback
func canonicalCachePayload(payload map[string]any) string {
	canonical, err := canonicalJSON(payload)
	if err != nil {
		// 兜底使用 fmt 格式，确保 key 不为空且低碰撞风险
		return fmt.Sprintf("fallback:%#v", normalizeValue(payload))
	}
	return canonical
}

// canonicalJSON 先规范化不可序列化值，再生成稳定 JSON 字符串
func canonicalJSON(v any) (string, error) {
	n := normalizeValue(v)
	buf, err := json.Marshal(n)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// normalizeValue 仅做可序列化防御，不再手工排序 map key（encoding/json 已稳定排序）
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

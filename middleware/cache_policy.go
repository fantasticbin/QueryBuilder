package middleware

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// CacheKeyBuilder 定义缓存键构建接口，业务方可覆写默认实现。
type CacheKeyBuilder interface {
	Build(ctx context.Context, meta core.QueryMeta) string
}

// CacheKeyHints 用于补充默认缓存键维度（如 filter/sort）。
// 如需多租户隔离，建议通过 Extra 传入 tenant_id 等稳定字段。
// 注意：Filter/Sort 建议传入可稳定序列化的值（map/struct/切片/标量）；
// 若传入函数、channel 等不可 JSON 序列化值，会自动降级为字符串表示，避免 key 空串碰撞。
type CacheKeyHints struct {
	Filter any
	Sort   any
	Extra  map[string]any
}

// DefaultCacheKeyBuilder 为缓存中间件提供开箱即用的默认 key 方案。
// Prefix 建议设置为业务资源名（如 "users"、"orders"），避免不同查询场景共享 key 空间。
// Hints 为缓存键补充维度，由调用方在创建 DefaultCacheKeyBuilder 时注入，
// 对于 Clone 并发场景，每个 Clone 实例各自 Use 携带不同 Hints 的缓存中间件即可。
type DefaultCacheKeyBuilder struct {
	Prefix string
	// Hints 缓存键补充维度（filter/sort/extra），由调用方在构建时注入
	Hints CacheKeyHints
	// HintsProvider 在 hints 为空时调用，用于减少调用方遗漏。
	HintsProvider func(context.Context) CacheKeyHints
}

// Build 根据查询元信息和 Hints 构建确定性、抗碰撞的缓存键。
// 内部将 prefix、datasource、fields、pagination 及 hints（filter/sort/extra）组装为规范化 JSON，
// 再取 SHA1 摘要生成最终 key，格式为 "qb:cache:<hex>"。
func (b DefaultCacheKeyBuilder) Build(ctx context.Context, meta core.QueryMeta) string {
	payload := map[string]any{"prefix": b.Prefix}
	payload["datasource"] = meta.DataSource
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
	}

	// 确定 hints：优先使用静态 Hints，为空时尝试 HintsProvider
	hints := b.Hints
	if hints.Filter == nil && hints.Sort == nil && len(hints.Extra) == 0 && b.HintsProvider != nil {
		hints = b.HintsProvider(ctx)
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

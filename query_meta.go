package builder

import (
	"context"
	"time"
)

// queryMetaKey 是 QueryMeta 在 context 中的 key 类型（未导出，防止外部冲突）
type queryMetaKey struct{}

// QueryMeta 查询元信息结构体
// 在执行查询前自动注入到 context 中，中间件可通过 QueryMetaFromContext 获取
type QueryMeta struct {
	DataSource     DataSource // 数据源类型
	Start          uint32     // 分页起始位置
	Limit          uint32     // 每页数据条数
	NeedTotal      bool       // 是否需要查询总数
	NeedPagination bool       // 是否需要分页
	Fields         []string   // 查询字段投影
	StartTime      time.Time  // 查询开始时间
}

// withQueryMeta 将 QueryMeta 注入到 context 中（未导出，仅内部使用）
func withQueryMeta(ctx context.Context, meta *QueryMeta) context.Context {
	return context.WithValue(ctx, queryMetaKey{}, meta)
}

// QueryMetaFromContext 从 context 中提取 QueryMeta
// 如果 context 中不存在 QueryMeta，返回 nil
func QueryMetaFromContext(ctx context.Context) *QueryMeta {
	meta, _ := ctx.Value(queryMetaKey{}).(*QueryMeta)
	return meta
}

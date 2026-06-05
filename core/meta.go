package core

import "time"

// QueryMeta 查询元信息结构体
// 中间件可通过 builder.GetQueryMeta() 获取当前查询的元数据快照
type QueryMeta struct {
	DataSource     DataSource // 数据源类型
	Start          uint32     // 分页起始位置
	Limit          uint32     // 每页数据条数
	NeedTotal      bool       // 是否需要查询总数
	NeedPagination bool       // 是否需要分页
	Fields         []string   // 查询字段投影
	IsCursorQuery  bool       // 是否为游标查询模式
	CursorFields   []string   // 游标分页排序字段列表
	CursorValues   []any      // 游标初始值（外部传入，用于断点续查/App分页场景）
	StartTime      time.Time  // 查询开始时间
}

// QuerierMeta 查询元信息能力接口
// 实现此接口的类型可提供查询元信息快照
type QuerierMeta interface {
	// GetQueryMeta 返回当前查询元信息的只读快照
	GetQueryMeta() QueryMeta
}

package core

// Result 查询结果接口，包含列表查询结果和游标分页查询结果两种类型
// 泛型参数:
//
//	R: 查询结果的实体类型
type Result[R any] interface {
	GetResultKind() ResultKind
	GetItems() []*R
	GetTotal() int64
	GetHasMore() bool
	GetNextCursorValues() []any
}

// ResultKind 查询结果类型
type ResultKind int

const (
	// ResultKindList 表示普通列表查询结果
	ResultKindList ResultKind = iota
	// ResultKindCursorPage 表示单批次游标分页查询结果
	ResultKindCursorPage
)

// ListResult 列表查询结果结构体
// 泛型参数:
//
//	R: 查询结果的实体类型
type ListResult[R any] struct {
	Items []*R  // 当前页的数据列表
	Total int64 // 总数（仅在 needTotal=true 时有效）
}

// GetResultKind 返回结果类型
func (r *ListResult[R]) GetResultKind() ResultKind {
	return ResultKindList
}

// GetItems 返回结果列表
func (r *ListResult[R]) GetItems() []*R {
	if r == nil {
		return nil
	}
	return r.Items
}

// GetTotal 返回总数
func (r *ListResult[R]) GetTotal() int64 {
	if r == nil {
		return 0
	}
	return r.Total
}

// GetHasMore 列表查询结果不支持 HasMore，始终返回 false
func (r *ListResult[R]) GetHasMore() bool {
	return false
}

// GetNextCursorValues 列表查询结果不支持游标值，始终返回 nil
func (r *ListResult[R]) GetNextCursorValues() []any {
	return nil
}

// CursorPageResult 游标分页查询结果结构体
// 用于 QueryPage 方法的返回值，提供单批次分页查询的结构化结果
// 泛型参数:
//
//	R: 查询结果的实体类型
type CursorPageResult[R any] struct {
	Items            []*R  // 当前页的数据列表
	Total            int64 // 总数（仅在 needTotal=true 时有效）
	HasMore          bool  // 是否还有下一页数据
	NextCursorValues []any // 下一页的游标值（用于传入下次查询的 SetCursorValue），HasMore=false 时为 nil
}

// GetResultKind 返回结果类型
func (r *CursorPageResult[R]) GetResultKind() ResultKind {
	return ResultKindCursorPage
}

// GetItems 返回结果列表
func (r *CursorPageResult[R]) GetItems() []*R {
	if r == nil {
		return nil
	}
	return r.Items
}

// GetTotal 返回总数
func (r *CursorPageResult[R]) GetTotal() int64 {
	if r == nil {
		return 0
	}
	return r.Total
}

// GetHasMore 返回是否有下一页
func (r *CursorPageResult[R]) GetHasMore() bool {
	if r == nil {
		return false
	}
	return r.HasMore
}

// GetNextCursorValues 返回下一页的游标值
func (r *CursorPageResult[R]) GetNextCursorValues() []any {
	if r == nil {
		return nil
	}
	return r.NextCursorValues
}

// ESPITPageResult ES PIT 分页查询结果。
// 内嵌 CursorPageResult 复用通用游标分页字段，额外提供 PitID 用于跨请求续查。
type ESPITPageResult[R any] struct {
	CursorPageResult[R]
	PitID string // Point-in-Time ID，用于下一批查询（HasMore=false 时为空）
}

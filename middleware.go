package builder

import "context"

// Middleware 查询中间件类型定义
// 参数:
//
//	ctx: 上下文
//	builder: 查询构建器实例
//	next: 下一个中间件或最终查询处理器
//
// 返回:
//
//	[]*R: 查询结果列表
//	int64: 总数
//	error: 错误信息
type Middleware[R any] func(
	ctx context.Context,
	builder *builder[R],
	next func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error)

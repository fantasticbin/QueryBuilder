package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// ConcurrentTask 表示一个受 context 控制的并发任务
type ConcurrentTask func(context.Context) error

// TaskEvent 描述一个并发任务的完成状态
type TaskEvent struct {
	Index    int
	Err      error
	Duration time.Duration
}

// panicError 包装一个已存在的 error，保留原始错误链
type panicError struct {
	prefix string
	err    error
}

func (e *panicError) Error() string { return e.prefix + ": " + e.err.Error() }

// Unwrap 返回被包装的原始错误，保留错误链供 errors.Is/errors.As 判定
func (e *panicError) Unwrap() error { return e.err }

// panicValueError 包装一个非 error 类型的 panic 值（如字符串、整型等）
type panicValueError struct {
	prefix string
	value  any
}

func (e *panicValueError) Error() string {
	return fmt.Sprintf("%s: %v", e.prefix, e.value)
}

// PanicToError 将 recover 捕获的值转换为 error
// 当 recovered 本身是 error 时，保留错误链，便于上层 errors.Is/errors.As 判定；
// 否则退化为文本描述。prefix 用于标注 panic 发生的上下文。
// 直接构造错误类型（而非 fmt.Errorf）可避免对 prefix/recovered 做反射装箱（[]any），
// 并降低函数复杂度以便编译器内联，减少 panic 恢复路径的分配与延迟。
func PanicToError(prefix string, recovered any) error {
	if err, ok := recovered.(error); ok {
		return &panicError{prefix: prefix, err: err}
	}
	return &panicValueError{prefix: prefix, value: recovered}
}

// WaitAndGo 等待所有函数执行完毕
func WaitAndGo(fn ...func() error) error {
	tasks := make([]ConcurrentTask, 0, len(fn))
	for _, f := range fn {
		tasks = append(tasks, func(context.Context) error {
			return f()
		})
	}
	return WaitAndGoWithContext(context.Background(), tasks...)
}

// WaitAndGoWithContext 等待所有受 context 控制的函数执行完毕
// 任一任务返回错误时，会取消同组任务的 context
func WaitAndGoWithContext(ctx context.Context, fn ...ConcurrentTask) error {
	_, done := GoWithNotify(ctx, fn...)
	return <-done
}

// GoWithNotify 并发执行任务，并通过事件 channel 通知每个任务的完成状态
// 返回的事件 channel 和 done channel 均由本函数关闭
func GoWithNotify(ctx context.Context, fn ...ConcurrentTask) (<-chan TaskEvent, <-chan error) {
	if ctx == nil {
		ctx = context.Background()
	}

	events := make(chan TaskEvent, len(fn))
	done := make(chan error, 1)

	g, groupCtx := errgroup.WithContext(ctx)
	for i, f := range fn {
		// Go 1.22+ 已不需要针对循环语义构造内部变量
		//i, f := i, f
		g.Go(func() (err error) {
			start := time.Now()
			defer func() {
				if r := recover(); r != nil {
					err = PanicToError("panic recovered", r)
				}
				events <- TaskEvent{
					Index:    i,
					Err:      err,
					Duration: time.Since(start),
				}
			}()
			if f == nil {
				return fmt.Errorf("nil concurrent task at index %d", i)
			}
			return f(groupCtx)
		})
	}

	go func() {
		done <- g.Wait()
		close(done)
		close(events)
	}()

	return events, done
}

// BuildString 使用 strings.Builder 将多个字符串片段拼接为单个字符串，并按片段总长度预分配缓冲区，
// 避免逐个 + 拼接产生的多次中间字符串分配（尤其在片段本身来自其他拼接结果时收益明显）。
// 等价于 strings.Join(parts, "")，但将“缓冲区预分配 + 拼接”逻辑集中在一处，便于复用与维护。
//
// 适用于游标查询等热路径中固定片段的 SQL 子句拼接（如 "col ASC"、"col > ?"）。
func BuildString(parts ...string) string {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	var sb strings.Builder
	sb.Grow(total)
	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

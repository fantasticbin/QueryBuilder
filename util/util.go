package util

import (
	"context"
	"fmt"
	"runtime/debug"
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

// PanicToError 将 recover 捕获的值转换为 error
// 当 recovered 本身是 error 时，使用 %w 保留错误链，便于上层 errors.Is/errors.As 判定；
// 否则退化为 %v 文本描述。prefix 用于标注 panic 发生的上下文。
func PanicToError(prefix string, recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %v", prefix, recovered)
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
					err = fmt.Errorf("panic recovered: %+v\n%s", r, string(debug.Stack()))
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

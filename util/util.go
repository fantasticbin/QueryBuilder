package util

import (
	"fmt"
	"runtime/debug"

	"golang.org/x/sync/errgroup"
)

// WaitAndGo 等待所有函数执行完毕
func WaitAndGo(fn ...func() error) error {
	var g errgroup.Group
	for _, f := range fn {
		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic recovered: %+v\n%s", r, string(debug.Stack()))
				}
			}()
			return f()
		})
	}
	return g.Wait()
}

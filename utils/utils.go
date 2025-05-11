package utils

import (
	"fmt"
	"golang.org/x/sync/errgroup"
	"runtime/debug"
)

// WaitAndGo 等待所有函数执行完毕
func WaitAndGo(fn ...func() error) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("Panic: %+v\n %s", err, string(debug.Stack()))
		}
	}()
	var g errgroup.Group
	for _, f := range fn {
		g.Go(func() error {
			return f()
		})
	}
	return g.Wait()
}

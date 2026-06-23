package test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// AssertNoError 在 err 不为 nil 时终止测试
func AssertNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// AssertErrorIs 使用 errors.Is 校验 err 是否匹配 target
func AssertErrorIs(t testing.TB, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

// AssertListResult 校验列表查询结果的 items 和 total
func AssertListResult[T any](
	t testing.TB,
	got *core.ListResult[T],
	wantItems []*T,
	wantTotal int64,
) {
	t.Helper()
	if got == nil {
		t.Fatal("expected list result, got nil")
	}
	assertResultPayload(t, got, wantItems, wantTotal)
}

// AssertCursorPageResult 校验游标分页结果的公共 payload 和游标元信息
func AssertCursorPageResult[T any](
	t testing.TB,
	got *core.CursorPageResult[T],
	wantItems []*T,
	wantTotal int64,
	wantHasMore bool,
	wantNextCursor []any,
) {
	t.Helper()
	if got == nil {
		t.Fatal("expected cursor page result, got nil")
	}
	assertResultPayload(t, got, wantItems, wantTotal)
	if got.HasMore != wantHasMore {
		t.Fatalf("expected hasMore %v, got %v", wantHasMore, got.HasMore)
	}
	if !reflect.DeepEqual(got.NextCursorValues, wantNextCursor) {
		t.Fatalf("expected next cursor %v, got %v", wantNextCursor, got.NextCursorValues)
	}
}

// AssertStringSliceEqual 校验两个字符串切片的长度和值是否一致
func AssertStringSliceEqual(t testing.TB, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected [%d]=%q, got %q in %v", i, want[i], got[i], got)
		}
	}
}

// PassthroughMiddleware 返回一个直接调用 next 的中间件
func PassthroughMiddleware[T any, Q any]() func(
	context.Context,
	Q,
	func(context.Context) (core.Result[T], error),
) (core.Result[T], error) {
	return func(
		ctx context.Context,
		builder Q,
		next func(context.Context) (core.Result[T], error),
	) (core.Result[T], error) {
		return next(ctx)
	}
}

// ListResultMiddleware 返回一个短路并直接产出列表结果的中间件
func ListResultMiddleware[T any, Q any](items []*T, total int64) func(
	context.Context,
	Q,
	func(context.Context) (core.Result[T], error),
) (core.Result[T], error) {
	return func(
		ctx context.Context,
		builder Q,
		next func(context.Context) (core.Result[T], error),
	) (core.Result[T], error) {
		return &core.ListResult[T]{Items: items, Total: total}, nil
	}
}

// EmptyListMiddleware 返回一个短路并直接产出空列表结果的中间件
func EmptyListMiddleware[T any, Q any]() func(
	context.Context,
	Q,
	func(context.Context) (core.Result[T], error),
) (core.Result[T], error) {
	return ListResultMiddleware[T, Q](nil, 0)
}

// assertResultPayload 校验查询结果公共的 total 和 items 内容
func assertResultPayload[T any](
	t testing.TB,
	got core.Result[T],
	wantItems []*T,
	wantTotal int64,
) {
	t.Helper()
	if got.GetTotal() != wantTotal {
		t.Fatalf("expected total %d, got %d", wantTotal, got.GetTotal())
	}

	gotItems := got.GetItems()
	if len(gotItems) != len(wantItems) {
		t.Fatalf("expected result length %d, got %d", len(wantItems), len(gotItems))
	}
	for i := range wantItems {
		if !reflect.DeepEqual(gotItems[i], wantItems[i]) {
			t.Fatalf("expected result[%d] %+v, got %+v", i, wantItems[i], gotItems[i])
		}
	}
}

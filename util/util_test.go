package util

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWaitAndGoWithContextCancelsSiblings(t *testing.T) {
	errBoom := errors.New("boom")
	ready := make(chan struct{})
	cancelled := make(chan struct{})

	err := WaitAndGoWithContext(context.Background(),
		func(context.Context) error {
			<-ready
			return errBoom
		},
		func(ctx context.Context) error {
			close(ready)
			<-ctx.Done()
			close(cancelled)
			return nil
		},
	)
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected boom error, got %v", err)
	}

	select {
	case <-cancelled:
	default:
		t.Fatal("expected sibling task context to be cancelled")
	}
}

func TestGoWithNotifyEmitsEventsAndClosesChannels(t *testing.T) {
	errFailed := errors.New("failed")
	events, done := GoWithNotify(context.Background(),
		func(context.Context) error {
			return nil
		},
		func(context.Context) error {
			return errFailed
		},
	)

	gotEvents := make(map[int]TaskEvent)
	for event := range events {
		gotEvents[event.Index] = event
	}
	if len(gotEvents) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(gotEvents))
	}
	if gotEvents[0].Err != nil {
		t.Fatalf("expected task 0 to succeed, got %v", gotEvents[0].Err)
	}
	if !errors.Is(gotEvents[1].Err, errFailed) {
		t.Fatalf("expected task 1 failure, got %v", gotEvents[1].Err)
	}

	err, ok := <-done
	if !ok {
		t.Fatal("expected done channel to yield final error")
	}
	if !errors.Is(err, errFailed) {
		t.Fatalf("expected final failure, got %v", err)
	}
	if _, ok := <-done; ok {
		t.Fatal("expected done channel to be closed")
	}
}

func TestWaitAndGoWithContextRecoversPanic(t *testing.T) {
	err := WaitAndGoWithContext(context.Background(), func(context.Context) error {
		panic("kaboom")
	})
	if err == nil {
		t.Fatal("expected panic to be recovered as error")
	}
	if !strings.Contains(err.Error(), "panic recovered") ||
		!strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("unexpected panic error: %v", err)
	}
}

func TestWaitAndGoWithContextRecoversErrorPanicPreservesChain(t *testing.T) {
	root := errors.New("root cause")
	err := WaitAndGoWithContext(context.Background(), func(context.Context) error {
		panic(root)
	})
	if !errors.Is(err, root) {
		t.Fatalf("expected unwrapped panic error, got %v", err)
	}
}

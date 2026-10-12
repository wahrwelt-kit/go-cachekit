package cachekit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestCachedValueCanceledWaiterLeavesSharedLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		value := MustNewCachedValue[int]("scoreboard", time.Minute)
		release := make(chan struct{})
		var calls atomic.Int32
		load := func(ctx context.Context) (int, error) {
			calls.Add(1)
			<-release
			return 42, ctx.Err()
		}
		leaderCtx, cancelLeader := context.WithCancel(t.Context())
		defer cancelLeader()
		leader := make(chan error, 1)
		go func() { _, err := value.Get(leaderCtx, load); leader <- err }()
		synctest.Wait()
		waiterCtx, cancelWaiter := context.WithCancel(t.Context())
		defer cancelWaiter()
		waiter := make(chan error, 1)
		go func() { _, err := value.Get(waiterCtx, load); waiter <- err }()
		result := make(chan int, 1)
		go func() { got, _ := value.Get(t.Context(), load); result <- got }()
		synctest.Wait()
		cancelLeader()
		cancelWaiter()
		synctest.Wait()
		for _, done := range []<-chan error{leader, waiter} {
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("canceled request: %v", err)
				}
			default:
				t.Error("canceled request still waiting for shared load")
			}
		}
		close(release)
		if got := <-result; got != 42 {
			t.Errorf("surviving caller got %d", got)
		}
		if calls.Load() != 1 {
			t.Errorf("load called %d times", calls.Load())
		}
		got, ok := value.GetStale()
		if !ok || got != 42 {
			t.Errorf("shared result not cached: %d, %v", got, ok)
		}
	})
}

func TestCachedValueInvalidateDuringLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		value := MustNewCachedValue[int]("scoreboard", time.Minute)
		release := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			_, _ = value.Get(t.Context(), func(context.Context) (int, error) { <-release; return 1, nil })
		}()
		synctest.Wait()
		value.Invalidate()
		got, err := value.Get(t.Context(), func(context.Context) (int, error) { return 2, nil })
		if err != nil || got != 2 {
			t.Errorf("new generation: %d, %v", got, err)
		}
		close(release)
		<-finished
		got, ok := value.GetStale()
		if !ok || got != 2 {
			t.Errorf("old generation replaced fresh cache: %d, %v", got, ok)
		}
	})
}

func TestCachedValueCanceledContextDoesNotLoad(t *testing.T) {
	value := MustNewCachedValue[int]("scoreboard", time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := value.Get(ctx, func(context.Context) (int, error) { t.Error("load called"); return 0, nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Get: %v", err)
	}
}

func TestCachedValueRespectCancellationStopsSharedLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		value := MustNewCachedValue[int]("scoreboard", time.Minute, WithRespectCallerCancel(true))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		load := func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		leader, waiter := make(chan error, 1), make(chan error, 1)
		go func() { _, err := value.Get(ctx, load); leader <- err }()
		synctest.Wait()
		go func() { _, err := value.Get(t.Context(), load); waiter <- err }()
		synctest.Wait()
		cancel()
		for _, done := range []<-chan error{leader, waiter} {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("shared load cancellation: %v", err)
			}
		}
		if _, ok := value.GetStale(); ok {
			t.Error("failed load populated cache")
		}
	})
}

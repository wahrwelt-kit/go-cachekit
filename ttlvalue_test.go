package cachekit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedValue_Get_LoadsOnMiss(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	ctx := context.Background()
	loaded := false
	val, err := v.Get(ctx, func(context.Context) (int, error) {
		loaded = true
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.True(t, loaded)
}

func TestCachedValue_Get_ReturnsCached(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	ctx := context.Background()
	calls := 0
	load := func(context.Context) (int, error) {
		calls++
		return 1, nil
	}
	val1, err := v.Get(ctx, load)
	require.NoError(t, err)
	assert.Equal(t, 1, val1)
	val2, err := v.Get(ctx, load)
	require.NoError(t, err)
	assert.Equal(t, 1, val2)
	assert.Equal(t, 1, calls)
}

func TestCachedValue_Invalidate(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	ctx := context.Background()
	v.Get(ctx, func(context.Context) (int, error) { return 10, nil }) //nolint:revive // priming cache; error not relevant
	_, ok := v.GetStale()
	require.True(t, ok)
	v.Invalidate()
	_, ok = v.GetStale()
	assert.False(t, ok)
	calls := 0
	val, err := v.Get(ctx, func(context.Context) (int, error) {
		calls++
		return 20, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 20, val)
	assert.Equal(t, 1, calls)
}

func TestCachedValue_GetStale(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	_, ok := v.GetStale()
	assert.False(t, ok)
	ctx := context.Background()
	v.Get(ctx, func(context.Context) (int, error) { return 7, nil }) //nolint:revive // priming cache; error not relevant
	val, ok := v.GetStale()
	require.True(t, ok)
	assert.Equal(t, 7, val)
}

func TestCachedValue_GetStale_SurvivesTTLExpiry(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", 50*time.Millisecond)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = v.Get(ctx, func(context.Context) (int, error) { return 99, nil })
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)
	val, ok := v.GetStale()
	require.True(t, ok)
	assert.Equal(t, 99, val)
}

func TestCachedValue_Get_ReloadsAfterTTLExpiry(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", 30*time.Millisecond)
	require.NoError(t, err)
	ctx := context.Background()
	calls := 0
	val, err := v.Get(ctx, func(context.Context) (int, error) {
		calls++
		return calls, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, val)
	time.Sleep(80 * time.Millisecond)
	val, err = v.Get(ctx, func(context.Context) (int, error) {
		calls++
		return calls, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, val)
	assert.Equal(t, 2, calls)
}

func TestCachedValue_Get_LoadError(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	loadErr := errors.New("load failed")
	_, err = v.Get(context.Background(), func(context.Context) (int, error) {
		return 0, loadErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
}

func TestNewCachedValue_InvalidTTL(t *testing.T) {
	t.Parallel()
	_, err := NewCachedValue[int]("k", 0)
	require.Error(t, err)
	_, err = NewCachedValue[int]("k", -time.Second)
	require.Error(t, err)
}

func TestNewCachedValue_EmptyKey(t *testing.T) {
	t.Parallel()
	_, err := NewCachedValue[int]("", time.Minute)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestCachedValue_WithLoadTimeout(t *testing.T) {
	t.Parallel()
	timeout := 50 * time.Millisecond
	v, err := NewCachedValue[int]("k", time.Minute, WithLoadTimeout(timeout))
	require.NoError(t, err)
	loadStarted := make(chan struct{})
	loadDone := make(chan struct{})
	_, err = v.Get(context.Background(), func(ctx context.Context) (int, error) {
		close(loadStarted)
		<-ctx.Done()
		close(loadDone)
		return 0, ctx.Err()
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	<-loadDone
}

func TestCachedValue_Get_NilContext(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	_, err = v.Get(nilContext(), func(context.Context) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrNilContext)
}

func TestCachedValue_Get_NilLoadFunc(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int]("k", time.Minute)
	require.NoError(t, err)
	_, err = v.Get(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilLoadFunc)
}

func TestCachedValue_WithRespectCallerCancel(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValue[int](
		"k",
		time.Minute,
		WithLoadTimeout(time.Second),
		WithRespectCallerCancel(true),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = v.Get(ctx, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	require.ErrorIs(t, err, context.Canceled)
}

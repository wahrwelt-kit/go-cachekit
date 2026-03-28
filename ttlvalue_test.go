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
	v := NewCachedValue[int](context.Background(), "k", time.Minute)
	t.Cleanup(v.Stop)
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
	v := NewCachedValue[int](context.Background(), "k", time.Minute)
	t.Cleanup(v.Stop)
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
	v := NewCachedValue[int](context.Background(), "k", time.Minute)
	t.Cleanup(v.Stop)
	ctx := context.Background()
	v.Get(ctx, func(context.Context) (int, error) { return 10, nil })
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
	v := NewCachedValue[int](context.Background(), "k", time.Minute)
	t.Cleanup(v.Stop)
	_, ok := v.GetStale()
	assert.False(t, ok)
	ctx := context.Background()
	v.Get(ctx, func(context.Context) (int, error) { return 7, nil })
	val, ok := v.GetStale()
	require.True(t, ok)
	assert.Equal(t, 7, val)
}

func TestCachedValue_GetStale_SurvivesTTLExpiry(t *testing.T) {
	t.Parallel()
	v := NewCachedValue[int](context.Background(), "k", 50*time.Millisecond)
	t.Cleanup(v.Stop)
	ctx := context.Background()
	_, err := v.Get(ctx, func(context.Context) (int, error) { return 99, nil })
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)
	assert.Nil(t, v.c.Get(v.key))
	val, ok := v.GetStale()
	require.True(t, ok)
	assert.Equal(t, 99, val)
}

func TestCachedValue_Get_LoadError(t *testing.T) {
	t.Parallel()
	v := NewCachedValue[int](context.Background(), "k", time.Minute)
	t.Cleanup(v.Stop)
	loadErr := errors.New("load failed")
	_, err := v.Get(context.Background(), func(context.Context) (int, error) {
		return 0, loadErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
}

func TestNewCachedValueE_InvalidTTL(t *testing.T) {
	t.Parallel()
	_, err := NewCachedValueE[int](context.Background(), "k", 0)
	require.Error(t, err)
	_, err = NewCachedValueE[int](context.Background(), "k", -time.Second)
	require.Error(t, err)
}

func TestNewCachedValueE_EmptyKey(t *testing.T) {
	t.Parallel()
	_, err := NewCachedValueE[int](context.Background(), "", time.Minute)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestCachedValue_Stop_DoubleCall(t *testing.T) {
	t.Parallel()
	v, err := NewCachedValueE[int](context.Background(), "k", time.Minute)
	require.NoError(t, err)
	v.Stop()
	v.Stop()
}

func TestCachedValue_WithLoadTimeout(t *testing.T) {
	t.Parallel()
	timeout := 50 * time.Millisecond
	v, err := NewCachedValueE[int](context.Background(), "k", time.Minute, WithLoadTimeout(timeout))
	require.NoError(t, err)
	t.Cleanup(v.Stop)
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

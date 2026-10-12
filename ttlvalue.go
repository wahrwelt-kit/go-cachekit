package cachekit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const defaultLoadTimeout = 30 * time.Second

// cachedValueConfig is the configuration for a CachedValue.
type cachedValueConfig struct {
	loadTimeout         time.Duration
	respectCallerCancel bool
}

// CachedValueOption configures a CachedValue at construction (e.g. WithLoadTimeout).
type CachedValueOption interface {
	applyCachedValue(*cachedValueConfig)
}

type cachedValueOptionFunc func(*cachedValueConfig)

func (f cachedValueOptionFunc) applyCachedValue(c *cachedValueConfig) {
	f(c)
}

// WithLoadTimeout sets the context timeout for the load function used in Get. Zero or negative means default 30s.
func WithLoadTimeout(d time.Duration) CachedValueOption {
	return cachedValueOptionFunc(func(c *cachedValueConfig) { c.loadTimeout = d })
}

func (o respectCallerCancelOption) applyCachedValue(cfg *cachedValueConfig) {
	cfg.respectCallerCancel = bool(o)
}

// CachedValue caches a single value by key with TTL and singleflight. Concurrent Get calls for the same key share one load.
type CachedValue[T any] struct {
	sf            singleflight.Group
	key           string
	ttl           time.Duration
	loadTimeout   time.Duration
	respectCancel bool
	version       atomic.Uint64
	mu            sync.RWMutex
	value         T
	expiresAt     time.Time
	hasValue      bool
	lastGood      T
	hasLastGood   bool
}

// NewCachedValue returns a CachedValue and an error if key is empty or ttl is not positive.
func NewCachedValue[T any](key string, ttl time.Duration, opts ...CachedValueOption) (*CachedValue[T], error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("cache NewCachedValue: %w, got %v", ErrInvalidTTL, ttl)
	}
	cfg := &cachedValueConfig{loadTimeout: defaultLoadTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt.applyCachedValue(cfg)
		}
	}
	loadTimeout := cfg.loadTimeout
	if loadTimeout <= 0 {
		loadTimeout = defaultLoadTimeout
	}
	return &CachedValue[T]{
		key:           key,
		ttl:           ttl,
		loadTimeout:   loadTimeout,
		respectCancel: cfg.respectCallerCancel,
	}, nil
}

// MustNewCachedValue returns a CachedValue or panics on invalid input. Use only with static configuration.
func MustNewCachedValue[T any](key string, ttl time.Duration, opts ...CachedValueOption) *CachedValue[T] {
	v, err := NewCachedValue[T](key, ttl, opts...)
	if err != nil {
		panic(err)
	}
	return v
}

func (v *CachedValue[T]) cached(now time.Time) (T, bool) {
	var zero T
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.hasValue || !now.Before(v.expiresAt) {
		return zero, false
	}
	return v.value, true
}

func (v *CachedValue[T]) storeFresh(value T, expiresAt time.Time) {
	v.value = value
	v.expiresAt = expiresAt
	v.hasValue = true
	v.lastGood = value
	v.hasLastGood = true
}

// Get returns the cached value or shares a load with concurrent callers.
// Each caller can stop waiting by canceling ctx. By default the shared load
// continues with context.WithoutCancel(ctx), bounded by WithLoadTimeout.
// WithRespectCallerCancel(true) also cancels the load when its initiating caller cancels.
// Nil receivers return ErrNilCachedValue; invalid result types return ErrUnexpectedType.
func (v *CachedValue[T]) Get(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if v == nil {
		return zero, ErrNilCachedValue
	}
	if ctx == nil {
		return zero, ErrNilContext
	}
	if load == nil {
		return zero, ErrNilLoadFunc
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if cached, ok := v.cached(time.Now()); ok {
		return cached, nil
	}
	// Schedule the load before Invalidate can forget this generation.
	v.mu.RLock()
	verBefore := v.version.Load()
	result := v.sf.DoChan(v.key, func() (any, error) {
		return v.loadFresh(ctx, verBefore, load)
	})
	v.mu.RUnlock()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-result:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if res.Err != nil {
			return zero, res.Err
		}
		typed, ok := res.Val.(T)
		if !ok && res.Val != nil {
			return zero, ErrUnexpectedType
		}
		return typed, nil
	}
}

func (v *CachedValue[T]) loadFresh(ctx context.Context, version uint64, load func(context.Context) (T, error)) (any, error) {
	if cached, ok := v.cached(time.Now()); ok {
		return cached, nil
	}
	if !v.respectCancel {
		ctx = context.WithoutCancel(ctx)
	}
	loadCtx, cancel := context.WithTimeout(ctx, v.loadTimeout)
	defer cancel()
	value, err := load(loadCtx)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	if v.version.Load() == version {
		v.storeFresh(value, time.Now().Add(v.ttl))
	}
	v.mu.Unlock()
	return value, nil
}

// GetStale returns the in-TTL value if present; otherwise the last successfully loaded value (survives TTL expiry); otherwise (zero, false). Does not call load. Nil receiver returns (zero, false).
func (v *CachedValue[T]) GetStale() (T, bool) {
	var zero T
	if v == nil {
		return zero, false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.hasValue && time.Now().Before(v.expiresAt) {
		return v.value, true
	}
	if v.hasLastGood {
		return v.lastGood, true
	}
	return zero, false
}

// Invalidate removes the cached value and forgets the singleflight key so the next Get will call load again. An in-flight Get that finishes after Invalidate will not write its result back. No-op if the receiver is nil.
func (v *CachedValue[T]) Invalidate() {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.version.Add(1)
	v.sf.Forget(v.key)
	var zero T
	v.value = zero
	v.expiresAt = time.Time{}
	v.hasValue = false
	v.lastGood = zero
	v.hasLastGood = false
	v.mu.Unlock()
}

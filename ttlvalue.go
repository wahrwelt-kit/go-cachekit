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

// Get returns the cached value if present; otherwise calls load with a timeout (see WithLoadTimeout), caches the result with the configured TTL, and returns it. Concurrent calls for the same key share one load (singleflight). Returns ErrNilCachedValue if the receiver is nil, or ErrUnexpectedType on type mismatch. By default load runs with context.WithoutCancel(ctx) so it can finish even if the caller cancels; pass WithRespectCallerCancel(true) at construction to make load observe caller cancellation.
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
	if cached, ok := v.cached(time.Now()); ok {
		return cached, nil
	}
	res, err, _ := v.sf.Do(v.key, func() (any, error) {
		if cached, ok := v.cached(time.Now()); ok {
			return cached, nil
		}
		verBefore := v.version.Load()
		baseCtx := ctx
		if !v.respectCancel {
			baseCtx = context.WithoutCancel(ctx)
		}
		loadCtx, cancel := context.WithTimeout(baseCtx, v.loadTimeout)
		val, err := load(loadCtx)
		cancel()
		if err != nil {
			return nil, err
		}
		v.mu.Lock()
		if v.version.Load() == verBefore {
			v.storeFresh(val, time.Now().Add(v.ttl))
		}
		v.mu.Unlock()
		return val, nil
	})
	if err != nil {
		return zero, err
	}
	typed, ok := res.(T)
	if !ok && res != nil {
		return zero, ErrUnexpectedType
	}
	return typed, nil
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

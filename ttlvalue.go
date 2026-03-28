package cachekit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"
)

const defaultLoadTimeout = 30 * time.Second

type staleEntry[T any] struct {
	val T
}

// cachedValueConfig is the configuration for a CachedValue
type cachedValueConfig struct {
	loadTimeout time.Duration
}

// CachedValueOption configures a CachedValue at construction (e.g. WithLoadTimeout)
type CachedValueOption func(*cachedValueConfig)

// WithLoadTimeout sets the context timeout for the load function used in Get. Zero or negative means default 30s
func WithLoadTimeout(d time.Duration) CachedValueOption {
	return func(c *cachedValueConfig) { c.loadTimeout = d }
}

// CachedValue caches a single value by key with TTL and singleflight. Concurrent Get calls for the same key share one load. When ctx passed to the constructor is cancelled, the internal TTL goroutine stops; if ctx is context.Background(), call Stop() when done to avoid goroutine leaks
type CachedValue[T any] struct {
	c           *ttlcache.Cache[string, T]
	sf          singleflight.Group
	key         string
	ttl         time.Duration
	loadTimeout time.Duration
	done        chan struct{}
	once        sync.Once
	version     atomic.Uint64
	lastGood    atomic.Pointer[staleEntry[T]]
	mu          sync.Mutex
}

// NewCachedValue returns a CachedValue for the given key and ttl. Panics if ttl <= 0 or key is empty. Use NewCachedValueE when ttl or key are config-driven to get an error instead of panicking. When ctx is cancelled the internal goroutine stops; with context.Background() the caller must call Stop() when done
func NewCachedValue[T any](ctx context.Context, key string, ttl time.Duration, opts ...CachedValueOption) *CachedValue[T] {
	v, err := NewCachedValueE[T](ctx, key, ttl, opts...)
	if err != nil {
		panic(err)
	}
	return v
}

// NewCachedValueE returns a CachedValue and an error if key is empty or ttl is not positive. Prefer over NewCachedValue when key or ttl are from configuration
func NewCachedValueE[T any](ctx context.Context, key string, ttl time.Duration, opts ...CachedValueOption) (*CachedValue[T], error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("cache NewCachedValue: %w, got %v", ErrInvalidTTL, ttl)
	}
	cfg := &cachedValueConfig{loadTimeout: defaultLoadTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	loadTimeout := cfg.loadTimeout
	if loadTimeout <= 0 {
		loadTimeout = defaultLoadTimeout
	}
	c := ttlcache.New(
		ttlcache.WithTTL[string, T](ttl),
		ttlcache.WithCapacity[string, T](1),
		ttlcache.WithDisableTouchOnHit[string, T](),
	)
	v := &CachedValue[T]{c: c, key: key, ttl: ttl, loadTimeout: loadTimeout, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
		case <-v.done:
		}
		c.Stop()
	}()
	go c.Start()
	return v, nil
}

// Get returns the cached value if present; otherwise calls load with a timeout (see WithLoadTimeout), caches the result with the configured TTL, and returns it. Concurrent calls for the same key share one load (singleflight). Returns ErrNilCachedValue if the receiver is nil, or ErrUnexpectedType on type mismatch. Load runs with context.WithoutCancel(ctx) so it can finish even if the caller cancels
func (v *CachedValue[T]) Get(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if v == nil {
		return zero, ErrNilCachedValue
	}
	if item := v.c.Get(v.key); item != nil {
		return item.Value(), nil
	}
	res, err, _ := v.sf.Do(v.key, func() (any, error) {
		if item := v.c.Get(v.key); item != nil {
			return item.Value(), nil
		}
		verBefore := v.version.Load()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), v.loadTimeout)
		val, err := load(loadCtx)
		cancel()
		if err != nil {
			return zero, err
		}
		v.mu.Lock()
		if v.version.Load() == verBefore {
			v.c.Set(v.key, val, v.ttl)
			v.lastGood.Store(&staleEntry[T]{val: val})
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

// GetStale returns the in-TTL value from the internal cache if present; otherwise the last successfully loaded value (survives TTL eviction); otherwise (zero, false). Does not call load. Nil receiver returns (zero, false)
func (v *CachedValue[T]) GetStale() (T, bool) {
	var zero T
	if v == nil {
		return zero, false
	}
	if item := v.c.Get(v.key); item != nil {
		return item.Value(), true
	}
	if entry := v.lastGood.Load(); entry != nil {
		return entry.val, true
	}
	return zero, false
}

// Invalidate removes the cached value and forgets the singleflight key so the next Get will call load again. An in-flight Get that finishes after Invalidate will not write its result back. No-op if the receiver is nil
func (v *CachedValue[T]) Invalidate() {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.version.Add(1)
	v.sf.Forget(v.key)
	v.c.Delete(v.key)
	v.lastGood.Store(nil)
	v.mu.Unlock()
}

// Stop stops the internal TTL cache goroutine and releases resources. Call when the CachedValue is no longer needed, especially when constructed with context.Background(). Safe to call multiple times; subsequent calls are no-ops. No-op if the receiver is nil
func (v *CachedValue[T]) Stop() {
	if v == nil {
		return
	}
	v.once.Do(func() { close(v.done) })
}

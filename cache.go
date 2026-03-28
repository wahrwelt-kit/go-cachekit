package cachekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	defaultGetOrLoadTimeout     = 30 * time.Second
	defaultMaxVersionMapEntries = 65536
	evictCollectCap             = 4096
)

type inFlightEntry struct {
	count atomic.Int32
}

// Cache provides Redis-backed JSON get/set with singleflight for GetOrLoad
// Use each key with a single type T; mixing types for the same key causes errors
// Del and DeleteByPrefix increment a per-key version; an in-flight load that finishes after Del will not write back
// Key versions are stored in a sync.Map. When the map size exceeds max entries (default 65536), excess entries are evicted (no ordering guarantee). Keys currently in flight for GetOrLoad are never evicted. Set WithMaxVersionMapEntries(0) for no limit (not recommended for long-lived instances)
type Cache struct {
	redis                *redis.Client
	sf                   singleflight.Group
	versionMap           sync.Map
	versionMapSize       atomic.Int64
	inFlightKeys         sync.Map
	maxVersionMapEntries int
	evictMu              sync.Mutex
}

// CacheOption configures a Cache at construction (e.g. WithMaxVersionMapEntries). Nil options are ignored
type CacheOption func(*Cache)

func escapeRedisGlob(s string) string {
	if !strings.ContainsAny(s, `\*?[]`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// WithMaxVersionMapEntries limits the in-memory version map size; when exceeded, excess entries are evicted in no particular order. Zero means no limit (not recommended for long-lived processes). Default is 65536
func WithMaxVersionMapEntries(n int) CacheOption {
	return func(c *Cache) {
		c.maxVersionMapEntries = n
	}
}

// New returns a Cache that uses the given Redis client for JSON get/set and singleflight. opts configure the cache (e.g. WithMaxVersionMapEntries). Nil options are ignored
func New(redis *redis.Client, opts ...CacheOption) *Cache {
	c := &Cache{redis: redis, maxVersionMapEntries: defaultMaxVersionMapEntries}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func addInFlight(c *Cache, key string) {
	for {
		e := &inFlightEntry{}
		e.count.Store(1)
		v, loaded := c.inFlightKeys.LoadOrStore(key, e)
		if !loaded {
			return
		}
		existing := v.(*inFlightEntry) //nolint:forcetypeassert // inFlightKeys only stores *inFlightEntry
		for {
			cur := existing.count.Load()
			if cur <= 0 {
				break
			}
			if existing.count.CompareAndSwap(cur, cur+1) {
				return
			}
		}
	}
}

func removeInFlight(c *Cache, key string) {
	v, ok := c.inFlightKeys.Load(key)
	if !ok {
		return
	}
	e := v.(*inFlightEntry) //nolint:forcetypeassert // inFlightKeys only stores *inFlightEntry
	for {
		cur := e.count.Load()
		if cur <= 0 {
			return
		}
		if e.count.CompareAndSwap(cur, cur-1) {
			if cur == 1 {
				if e.count.CompareAndSwap(0, -1) {
					c.inFlightKeys.Delete(key)
				}
			}
			return
		}
	}
}

func cacheKeyVersion(c *Cache, key string) *atomic.Uint64 {
	if v, ok := c.versionMap.Load(key); ok {
		return v.(*atomic.Uint64) //nolint:forcetypeassert // versionMap only stores *atomic.Uint64
	}
	newVer := &atomic.Uint64{}
	actual, loaded := c.versionMap.LoadOrStore(key, newVer)
	if !loaded {
		c.versionMapSize.Add(1)
		if c.maxVersionMapEntries != 0 && c.versionMapSize.Load() > int64(c.maxVersionMapEntries) {
			c.evictMu.Lock()
			evictVersionMapExcess(c, key)
			c.evictMu.Unlock()
		}
	}
	return actual.(*atomic.Uint64) //nolint:forcetypeassert // versionMap only stores *atomic.Uint64
}

func evictVersionMapExcess(c *Cache, protectedKey string) {
	if c.maxVersionMapEntries == 0 {
		return
	}
	keys := make([]string, 0, evictCollectCap)
	c.versionMap.Range(func(k, _ any) bool {
		if len(keys) >= evictCollectCap {
			return false
		}
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
		return true
	})
	evict := int(c.versionMapSize.Load()) - c.maxVersionMapEntries
	if evict <= 0 {
		return
	}
	if evict > len(keys) {
		evict = len(keys)
	}
	deleted := 0
	for i := 0; i < len(keys) && deleted < evict; i++ {
		k := keys[i]
		if k == protectedKey {
			continue
		}
		if v, ok := c.inFlightKeys.Load(k); ok {
			if e, ok := v.(*inFlightEntry); ok && e.count.Load() > 0 {
				continue
			}
		}
		c.versionMap.Delete(k)
		c.versionMapSize.Add(-1)
		deleted++
	}
}

// GetOrLoadOpts holds options for GetOrLoad. Use WithTimeout and WithRespectCallerCancel to configure
type GetOrLoadOpts struct {
	// Timeout is the context timeout for the load function and for Redis Set (default 30s). Must be positive
	Timeout time.Duration
	// RespectCallerCancel, when true, passes the caller's context to loadFn so cancellation aborts the load; when false, loadFn runs with context.WithoutCancel so it can finish and write back
	RespectCallerCancel bool
}

// GetOrLoadOption configures GetOrLoad (e.g. WithTimeout, WithRespectCallerCancel). Nil options are ignored
type GetOrLoadOption func(*GetOrLoadOpts)

// WithTimeout sets the context timeout for the load function and for Redis Set in GetOrLoad. Default is 30s. Only positive values are applied
func WithTimeout(d time.Duration) GetOrLoadOption {
	return func(o *GetOrLoadOpts) {
		if d > 0 {
			o.Timeout = d
		}
	}
}

// WithRespectCallerCancel controls whether loadFn in GetOrLoad receives the caller's context. When true, loadFn sees cancellation; when false (default), loadFn runs with context.WithoutCancel so it can finish and write the result to Redis even if the caller cancels
func WithRespectCallerCancel(respect bool) GetOrLoadOption {
	return func(o *GetOrLoadOpts) { o.RespectCallerCancel = respect }
}

// GetOrLoad returns the cached value for key or calls loadFn, stores the result with ttl, and returns it
// Key must be non-empty. Use the same key only with one type T; otherwise concurrent calls with different T may get a type error
// Del and DeleteByPrefix increment the key version so an in-flight load that completes after delete will not overwrite
// There is a small TOCTOU window between the version check and Redis Set-a concurrent Del in that window may be overwritten by a stale Set; consistency is best-effort and TTL limits staleness
// loadFn receives the request context and may respect context cancellation
// If loadFn succeeds but Redis Set fails, returns (loadedData, err): caller receives the data and the set error
// Optional opts: WithTimeout, WithRespectCallerCancel
func GetOrLoad[T any](c *Cache, ctx context.Context, key string, ttl time.Duration, loadFn func(context.Context) (T, error), opts ...GetOrLoadOption) (T, error) {
	var result T
	if c == nil || c.redis == nil {
		return result, ErrRedisNotConfigured
	}
	if key == "" {
		return result, ErrEmptyKey
	}
	if ttl <= 0 {
		return result, fmt.Errorf("cache GetOrLoad: %w, got %v", ErrInvalidTTL, ttl)
	}
	o := &GetOrLoadOpts{Timeout: defaultGetOrLoadTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultGetOrLoadTimeout
	}
	respectCallerCancel := o.RespectCallerCancel
	val, err := c.redis.Get(ctx, key).Result()
	if err == nil {
		if unmarshalErr := json.Unmarshal([]byte(val), &result); unmarshalErr != nil {
			c.sf.Forget(key)
			cacheKeyVersion(c, key).Add(1)
			delErr := c.redis.Del(ctx, key).Err()
			var zero T
			if delErr != nil {
				return zero, fmt.Errorf("cache get unmarshal: %w (del failed: %w)", unmarshalErr, delErr)
			}
			return zero, fmt.Errorf("cache get unmarshal: %w", unmarshalErr)
		}
		return result, nil
	}
	if !errors.Is(err, redis.Nil) {
		var zero T
		return zero, fmt.Errorf("cache get: %w", err)
	}
	addInFlight(c, key)
	defer removeInFlight(c, key)
	ver := cacheKeyVersion(c, key)
	baseCtx := ctx
	if !respectCallerCancel {
		baseCtx = context.WithoutCancel(ctx)
	}
	v, err, _ := c.sf.Do(key, func() (any, error) {
		verBefore := ver.Load()
		loadCtx, cancel := context.WithTimeout(baseCtx, timeout)
		data, err := loadFn(loadCtx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("cache load: %w", err)
		}
		if ver.Load() != verBefore {
			return data, nil
		}
		bytes, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return nil, fmt.Errorf("cache load marshal: %w", marshalErr)
		}
		setCtx, cancel := context.WithTimeout(baseCtx, timeout)
		setErr := c.redis.Set(setCtx, key, bytes, ttl).Err()
		cancel()
		if setErr != nil {
			return data, fmt.Errorf("cache set after load: %w", setErr)
		}
		return data, nil
	})

	cached, ok := v.(T)
	if !ok && v != nil {
		var zero T
		return zero, ErrUnexpectedType
	}
	if err != nil {
		var zero T
		if ok {
			return cached, err
		}
		return zero, err
	}
	return cached, nil
}

// Del deletes the given keys from Redis and forgets them in singleflight so subsequent GetOrLoad will reload
// All keys must be non-empty. Returns ErrRedisNotConfigured if the cache has no Redis client, ErrEmptyKey if any key is empty
func (c *Cache) Del(ctx context.Context, keys ...string) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if len(keys) == 0 {
		return nil
	}
	if slices.Contains(keys, "") {
		return ErrEmptyKey
	}
	for _, key := range keys {
		c.sf.Forget(key)
		cacheKeyVersion(c, key).Add(1)
	}
	return c.redis.Del(ctx, keys...).Err()
}

// Set marshals value as JSON and stores it in Redis with the given ttl
// Key must be non-empty; ttl must be positive. Also forgets the key in singleflight and increments the per-key version
// Returns ErrRedisNotConfigured, ErrEmptyKey, or ErrInvalidTTL on invalid input; errors from Redis are wrapped
func (c *Cache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if key == "" {
		return ErrEmptyKey
	}
	if ttl <= 0 {
		return fmt.Errorf("cache set: %w, got %v", ErrInvalidTTL, ttl)
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache set marshal: %w", err)
	}
	c.sf.Forget(key)
	cacheKeyVersion(c, key).Add(1)
	if err := c.redis.Set(ctx, key, bytes, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

const deleteByPrefixBatchSize = 500

// DeleteByPrefix scans keys matching prefix* and Unlinks them in Redis, and forgets each key in singleflight
// prefix must be non-empty; Redis glob characters in prefix are escaped. maxIterations is optional (variadic): pass one positive int to cap SCAN iterations and avoid blocking; 0 or omitted means no limit
// Returns ErrRedisNotConfigured or ErrEmptyPrefix on invalid input; scan/unlink errors are wrapped
func (c *Cache) DeleteByPrefix(ctx context.Context, prefix string, maxIterations ...int) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if prefix == "" {
		return ErrEmptyPrefix
	}
	var limit int
	if len(maxIterations) > 0 {
		limit = maxIterations[0]
	}
	match := escapeRedisGlob(prefix) + "*"
	var cursor uint64
	iterations := 0
	for limit == 0 || iterations < limit {
		keys, nextCursor, err := c.redis.Scan(ctx, cursor, match, deleteByPrefixBatchSize).Result()
		if err != nil {
			return fmt.Errorf("cache delete by prefix scan: %w", err)
		}
		if len(keys) > 0 {
			for _, key := range keys {
				c.sf.Forget(key)
				cacheKeyVersion(c, key).Add(1)
			}
			if err := c.redis.Unlink(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("cache delete by prefix unlink: %w", err)
			}
		}
		iterations++
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

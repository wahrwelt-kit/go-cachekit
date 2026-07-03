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
	redisKeyFencePrefix         = "\x00cachekit:mutation:key:"
	redisPrefixFencePrefix      = "\x00cachekit:mutation:prefix:"
	//nolint:gosec // Internal Redis cache metadata key prefix, not a credential.
	redisEntryTokenPrefix = "\x00cachekit:entry_token:"
	//nolint:dupword // Lua function contains repeated "end" tokens.
	luaMutationSnapshotFunction = `
local function snapshot(data_key, exact_key, prefix_fence_prefix)
	local exact = redis.call("GET", exact_key)
	if exact == false then
		exact = "0"
	end

	local parts = {"k", exact}
	local key_len = string.len(data_key)
	for i = 1, key_len do
		local prefix = string.sub(data_key, 1, i)
		local prefix_version = redis.call("GET", prefix_fence_prefix .. prefix)
		if prefix_version ~= false then
			table.insert(parts, "p")
			table.insert(parts, tostring(i))
			table.insert(parts, prefix)
			table.insert(parts, prefix_version)
		end
	end
	return table.concat(parts, "\31")
end
`
	mutationSnapshotScript = luaMutationSnapshotFunction + `
return snapshot(ARGV[1], KEYS[1], ARGV[2])
`
	getFreshValueScript = luaMutationSnapshotFunction + `
local value = redis.call("GET", KEYS[1])
if value == false then
	return {0}
end
local current = snapshot(ARGV[1], KEYS[3], ARGV[2])
local token = redis.call("GET", KEYS[2])
if token == false or token ~= current then
	return {0}
end
return {1, value}
`
	setLoadedValueScript = luaMutationSnapshotFunction + `
local current = snapshot(ARGV[1], KEYS[3], ARGV[2])
if current ~= ARGV[3] then
	return 0
end
redis.call("SET", KEYS[1], ARGV[4], "PX", ARGV[5])
redis.call("SET", KEYS[2], current, "PX", ARGV[5])
return 1
`
	setValueScript = luaMutationSnapshotFunction + `
redis.call("INCR", KEYS[3])
local current = snapshot(ARGV[1], KEYS[3], ARGV[2])
redis.call("SET", KEYS[1], ARGV[3], "PX", ARGV[4])
redis.call("SET", KEYS[2], current, "PX", ARGV[4])
return 1
`
	bumpPrefixFenceScript = `return redis.call("INCR", KEYS[1])`
	delKeysScript         = `
local n = tonumber(ARGV[1])
local deleted = 0
for i = 1, n do
	redis.call("INCR", KEYS[(2 * n) + i])
end
for i = 1, n do
	deleted = deleted + redis.call("DEL", KEYS[i], KEYS[n + i])
end
return deleted
`
	//nolint:dupword // Lua script contains repeated "end" tokens.
	unlinkStaleValuesScript = luaMutationSnapshotFunction + `
local n = tonumber(ARGV[1])
local prefix_fence_prefix = ARGV[2]
local deleted = 0
for i = 1, n do
	local current = snapshot(KEYS[i], KEYS[(2 * n) + i], prefix_fence_prefix)
	local token = redis.call("GET", KEYS[n + i])
	if token == false or token ~= current then
		deleted = deleted + redis.call("UNLINK", KEYS[i], KEYS[n + i])
	end
end
return deleted
`
)

var (
	mutationSnapshotRedisScript  = redis.NewScript(mutationSnapshotScript)
	getFreshValueRedisScript     = redis.NewScript(getFreshValueScript)
	setLoadedValueRedisScript    = redis.NewScript(setLoadedValueScript)
	setValueRedisScript          = redis.NewScript(setValueScript)
	bumpPrefixFenceRedisScript   = redis.NewScript(bumpPrefixFenceScript)
	delKeysRedisScript           = redis.NewScript(delKeysScript)
	unlinkStaleValuesRedisScript = redis.NewScript(unlinkStaleValuesScript)
)

type inFlightEntry struct {
	count atomic.Int32
}

// Cache provides Redis-backed JSON get/set with singleflight for GetOrLoad
// Use each key with a single type T; mixing types for the same key causes errors
// Del, Set, and DeleteByPrefix increment Redis mutation fences; in-flight loads that started before a matching mutation do not write back, and later calls use a new singleflight generation
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

func keyFenceKey(key string) string {
	return redisKeyFencePrefix + key
}

func prefixFenceKey(prefix string) string {
	return redisPrefixFencePrefix + prefix
}

func entryTokenKey(key string) string {
	return redisEntryTokenPrefix + key
}

func delScriptKeys(keys []string) []string {
	all := make([]string, 0, len(keys)*3)
	all = append(all, keys...)
	for _, key := range keys {
		all = append(all, entryTokenKey(key))
	}
	for _, key := range keys {
		all = append(all, keyFenceKey(key))
	}
	return all
}

func snapshotKeys(key string) []string {
	return []string{keyFenceKey(key)}
}

func valueScriptKeys(key string) []string {
	return []string{key, entryTokenKey(key), keyFenceKey(key)}
}

func mutationScriptArgs(key string, extra ...any) []any {
	args := make([]any, 0, 2+len(extra))
	args = append(args, key, redisPrefixFencePrefix)
	args = append(args, extra...)
	return args
}

// CacheOption configures a Cache at construction (e.g. WithMaxVersionMapEntries). Nil options are ignored
type CacheOption func(*Cache)

func escapeRedisGlob(s string) string {
	if !strings.ContainsAny(s, `\*?[]`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		ch := s[i]
		switch ch {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		default:
		}
		b.WriteByte(ch)
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
		existing := v.(*inFlightEntry) //nolint:forcetypeassert,errcheck,revive // inFlightKeys only stores *inFlightEntry
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
	e := v.(*inFlightEntry) //nolint:forcetypeassert,errcheck,revive // inFlightKeys only stores *inFlightEntry
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
		return v.(*atomic.Uint64) //nolint:forcetypeassert,errcheck,revive // versionMap only stores *atomic.Uint64
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
	return actual.(*atomic.Uint64) //nolint:forcetypeassert,errcheck,revive // versionMap only stores *atomic.Uint64
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
	// Timeout is the context timeout for the load function and Redis write-back (default 30s). Must be positive
	Timeout time.Duration
	// RespectCallerCancel, when true, passes the caller's context to loadFn so cancellation aborts the load; when false, loadFn runs with context.WithoutCancel so it can finish and write back
	RespectCallerCancel bool
	// BypassOnCacheError, when true, treats Redis cache read/snapshot/write errors as cache misses and returns loaded data if loadFn succeeds
	BypassOnCacheError bool
}

// GetOrLoadOption configures GetOrLoad (e.g. WithTimeout, WithRespectCallerCancel). Nil options are ignored
type GetOrLoadOption interface {
	applyGetOrLoad(*GetOrLoadOpts)
}

type getOrLoadOptionFunc func(*GetOrLoadOpts)

func (f getOrLoadOptionFunc) applyGetOrLoad(o *GetOrLoadOpts) {
	f(o)
}

// WithTimeout sets the context timeout for the load function and Redis write-back in GetOrLoad. Default is 30s. Only positive values are applied
func WithTimeout(d time.Duration) GetOrLoadOption {
	return getOrLoadOptionFunc(func(o *GetOrLoadOpts) {
		if d > 0 {
			o.Timeout = d
		}
	})
}

type respectCallerCancelOption bool

// RespectCallerCancelOption is accepted by both GetOrLoad and CachedValue constructors.
type RespectCallerCancelOption interface {
	GetOrLoadOption
	CachedValueOption
}

// WithRespectCallerCancel controls whether load functions receive the caller's context. It can be passed to GetOrLoad and NewCachedValue. When true, loadFn sees cancellation; when false (default), loadFn runs with context.WithoutCancel so it can finish and write the result even if the caller cancels
func WithRespectCallerCancel(respect bool) RespectCallerCancelOption {
	return respectCallerCancelOption(respect)
}

func (o respectCallerCancelOption) applyGetOrLoad(cfg *GetOrLoadOpts) {
	cfg.RespectCallerCancel = bool(o)
}

// WithBypassOnCacheError controls whether Redis cache errors are fatal. When true, GetOrLoad calls loadFn and returns loaded data if Redis read, mutation snapshot, or write-back fails
func WithBypassOnCacheError(bypass bool) GetOrLoadOption {
	return getOrLoadOptionFunc(func(o *GetOrLoadOpts) {
		o.BypassOnCacheError = bypass
	})
}

func resolveGetOrLoadOpts(opts ...GetOrLoadOption) GetOrLoadOpts {
	cfg := GetOrLoadOpts{Timeout: defaultGetOrLoadTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt.applyGetOrLoad(&cfg)
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultGetOrLoadTimeout
	}
	return cfg
}

func ttlMilliseconds(ttl time.Duration) int64 {
	ms := ttl.Milliseconds()
	if ms <= 0 {
		return 1
	}
	return ms
}

func (c *Cache) mutationSnapshot(ctx context.Context, key string) (string, error) {
	token, err := mutationSnapshotRedisScript.Run(ctx, c.redis, snapshotKeys(key), mutationScriptArgs(key)...).Text()
	if err != nil {
		return "", err
	}
	return token, nil
}

func (c *Cache) bumpPrefixMutationVersion(ctx context.Context, prefix string) error {
	if err := bumpPrefixFenceRedisScript.Run(ctx, c.redis, []string{prefixFenceKey(prefix)}).Err(); err != nil {
		return fmt.Errorf("cache prefix mutation version bump: %w", err)
	}
	return nil
}

func (c *Cache) setLoadedValueIfUnchanged(ctx context.Context, key string, value []byte, ttl time.Duration, expectedToken string) error {
	args := mutationScriptArgs(key, expectedToken, value, ttlMilliseconds(ttl))
	return setLoadedValueRedisScript.Run(ctx, c.redis, valueScriptKeys(key), args...).Err()
}

func singleflightKey(key, mutationToken string) string {
	return key + "\x00mutation:" + mutationToken
}

func (c *Cache) deleteCorruptCacheEntry(ctx context.Context, key string, unmarshalErr error) error {
	cacheKeyVersion(c, key).Add(1)
	if delErr := c.deleteKeys(ctx, []string{key}); delErr != nil {
		return fmt.Errorf("cache get unmarshal: %w (del failed: %w)", unmarshalErr, delErr)
	}
	return fmt.Errorf("cache get unmarshal: %w", unmarshalErr)
}

func readCachedJSON[T any](c *Cache, ctx context.Context, key string) (value T, hit bool, err error) {
	result, err := getFreshValueRedisScript.Run(ctx, c.redis, valueScriptKeys(key), mutationScriptArgs(key)...).Slice()
	if err != nil {
		var zero T
		return zero, false, fmt.Errorf("cache get: %w", err)
	}
	if len(result) == 0 {
		var zero T
		return zero, false, errors.New("cache get: malformed script result")
	}
	fresh, ok := result[0].(int64)
	if !ok {
		var zero T
		return zero, false, fmt.Errorf("cache get: unexpected freshness flag %T", result[0])
	}
	if fresh == 0 {
		return value, false, nil
	}
	if len(result) != 2 {
		var zero T
		return zero, false, errors.New("cache get: malformed hit result")
	}
	val, ok := result[1].(string)
	if !ok {
		if raw, ok := result[1].([]byte); ok {
			val = string(raw)
		} else {
			var zero T
			return zero, false, fmt.Errorf("cache get: unexpected value type %T", result[1])
		}
	}
	if unmarshalErr := json.Unmarshal([]byte(val), &value); unmarshalErr != nil {
		var zero T
		return zero, false, c.deleteCorruptCacheEntry(ctx, key, unmarshalErr)
	}
	return value, true, nil
}

func callLoadWithTimeout[T any](baseCtx context.Context, cfg GetOrLoadOpts, loadFn func(context.Context) (T, error)) (T, error) {
	loadCtx, cancel := context.WithTimeout(baseCtx, cfg.Timeout)
	data, err := loadFn(loadCtx)
	cancel()
	if err != nil {
		var zero T
		return zero, fmt.Errorf("cache load: %w", err)
	}
	return data, nil
}

func loadBypassingCache[T any](c *Cache, baseCtx context.Context, key string, cfg GetOrLoadOpts, loadFn func(context.Context) (T, error)) (T, error) {
	var zero T
	v, err, _ := c.sf.Do(key+"\x00cache-bypass", func() (any, error) {
		return callLoadWithTimeout(baseCtx, cfg, loadFn)
	})
	if err != nil {
		return zero, err
	}
	typed, ok := v.(T)
	if !ok && v != nil {
		return zero, ErrUnexpectedType
	}
	return typed, nil
}

func loadAndStoreIfFresh[T any](
	c *Cache,
	baseCtx context.Context,
	key string,
	ttl time.Duration,
	cfg GetOrLoadOpts,
	localVersion *atomic.Uint64,
	redisTokenBefore string,
	loadFn func(context.Context) (T, error),
) (T, error) {
	var zero T
	sfKey := singleflightKey(key, redisTokenBefore)
	v, err, _ := c.sf.Do(sfKey, func() (any, error) {
		verBefore := localVersion.Load()
		data, err := callLoadWithTimeout(baseCtx, cfg, loadFn)
		if err != nil {
			return nil, err
		}
		if localVersion.Load() != verBefore {
			return data, nil
		}
		bytes, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return nil, fmt.Errorf("cache load marshal: %w", marshalErr)
		}
		setCtx, cancel := context.WithTimeout(baseCtx, cfg.Timeout)
		setErr := c.setLoadedValueIfUnchanged(setCtx, key, bytes, ttl, redisTokenBefore)
		cancel()
		if setErr != nil {
			if cfg.BypassOnCacheError {
				return data, nil
			}
			return data, fmt.Errorf("cache set after load: %w", setErr) //nolint:nilnil // cache write-back failed but data is valid; caller gets value with non-fatal error
		}
		return data, nil
	})
	cached, ok := v.(T)
	if !ok && v != nil {
		return zero, ErrUnexpectedType
	}
	if err != nil {
		if ok {
			return cached, err
		}
		return zero, err
	}
	return cached, nil
}

func (c *Cache) deleteKeys(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := delKeysRedisScript.Run(ctx, c.redis, delScriptKeys(keys), len(keys)).Err(); err != nil {
		return fmt.Errorf("cache del: %w", err)
	}
	return nil
}

func (c *Cache) unlinkStaleValues(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := unlinkStaleValuesRedisScript.Run(ctx, c.redis, delScriptKeys(keys), len(keys), redisPrefixFencePrefix).Err(); err != nil {
		return fmt.Errorf("cache delete by prefix unlink: %w", err)
	}
	return nil
}

// GetOrLoad returns the cached value for key or calls loadFn, stores the result with ttl, and returns it
// Key must be non-empty. Use the same key only with one type T; otherwise concurrent calls with different T may get a type error
// Del, Set, and DeleteByPrefix increment Redis mutation fences. A load that completes after a matching mutation will not write back, and calls started after the mutation do not join older in-flight loads
// loadFn receives the request context and may respect context cancellation
// If loadFn succeeds but Redis write-back fails, returns (loadedData, err): caller receives the data and the write-back error
// Optional opts: WithTimeout, WithRespectCallerCancel, WithBypassOnCacheError
func GetOrLoad[T any](c *Cache, ctx context.Context, key string, ttl time.Duration, loadFn func(context.Context) (T, error), opts ...GetOrLoadOption) (T, error) { //nolint:cyclop,revive // cache loading logic requires multiple branching paths
	var result T
	if c == nil || c.redis == nil {
		return result, ErrRedisNotConfigured
	}
	if ctx == nil {
		return result, ErrNilContext
	}
	if key == "" {
		return result, ErrEmptyKey
	}
	if ttl <= 0 {
		return result, fmt.Errorf("cache GetOrLoad: %w, got %v", ErrInvalidTTL, ttl)
	}
	if loadFn == nil {
		return result, ErrNilLoadFunc
	}
	cfg := resolveGetOrLoadOpts(opts...)
	baseCtx := ctx
	if !cfg.RespectCallerCancel {
		baseCtx = context.WithoutCancel(ctx)
	}
	if cached, ok, err := readCachedJSON[T](c, ctx, key); ok || err != nil {
		if err != nil && cfg.BypassOnCacheError {
			return loadBypassingCache(c, baseCtx, key, cfg, loadFn)
		}
		return cached, err
	}
	versionCtx, cancel := context.WithTimeout(baseCtx, cfg.Timeout)
	redisTokenBefore, err := c.mutationSnapshot(versionCtx, key)
	cancel()
	if err != nil {
		if cfg.BypassOnCacheError {
			return loadBypassingCache(c, baseCtx, key, cfg, loadFn)
		}
		var zero T
		return zero, fmt.Errorf("cache mutation version: %w", err)
	}
	addInFlight(c, key)
	defer removeInFlight(c, key)
	ver := cacheKeyVersion(c, key)
	return loadAndStoreIfFresh(c, baseCtx, key, ttl, cfg, ver, redisTokenBefore, loadFn)
}

// Del deletes the given keys from Redis and advances each key fence so subsequent GetOrLoad calls reload
// All keys must be non-empty. Returns ErrRedisNotConfigured if the cache has no Redis client, ErrEmptyKey if any key is empty
func (c *Cache) Del(ctx context.Context, keys ...string) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if len(keys) == 0 {
		return nil
	}
	if ctx == nil {
		return ErrNilContext
	}
	if slices.Contains(keys, "") {
		return ErrEmptyKey
	}
	for _, key := range keys {
		cacheKeyVersion(c, key).Add(1)
	}
	return c.deleteKeys(ctx, keys)
}

// Set marshals value as JSON and stores it in Redis with the given ttl
// Key must be non-empty; ttl must be positive. Also advances the key fence so older in-flight GetOrLoad calls cannot overwrite it
// Returns ErrRedisNotConfigured, ErrEmptyKey, or ErrInvalidTTL on invalid input; errors from Redis are wrapped
func (c *Cache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if ctx == nil {
		return ErrNilContext
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
	cacheKeyVersion(c, key).Add(1)
	args := mutationScriptArgs(key, bytes, ttlMilliseconds(ttl))
	if err := setValueRedisScript.Run(ctx, c.redis, valueScriptKeys(key), args...).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

const deleteByPrefixBatchSize = 500

type deleteByPrefixOptions struct {
	limit int
}

// DeleteByPrefixOption configures DeleteByPrefix. Nil options are ignored.
type DeleteByPrefixOption interface {
	applyDeleteByPrefix(*deleteByPrefixOptions)
}

type deleteByPrefixOptionFunc func(*deleteByPrefixOptions)

func (f deleteByPrefixOptionFunc) applyDeleteByPrefix(o *deleteByPrefixOptions) {
	f(o)
}

// WithDeleteByPrefixLimit caps SCAN iterations for DeleteByPrefix. Zero or negative means no limit.
func WithDeleteByPrefixLimit(limit int) DeleteByPrefixOption {
	return deleteByPrefixOptionFunc(func(o *deleteByPrefixOptions) {
		if limit > 0 {
			o.limit = limit
		}
	})
}

// DeleteByPrefix advances the prefix fence, scans keys matching prefix*, and Unlinks them with their cache token keys
// prefix must be non-empty; Redis glob characters in prefix are escaped. Pass WithDeleteByPrefixLimit(n) to cap SCAN iterations and avoid blocking; omitted means no limit
// Returns ErrRedisNotConfigured or ErrEmptyPrefix on invalid input; scan/unlink errors are wrapped
func (c *Cache) DeleteByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) error {
	if c == nil || c.redis == nil {
		return ErrRedisNotConfigured
	}
	if ctx == nil {
		return ErrNilContext
	}
	if prefix == "" {
		return ErrEmptyPrefix
	}
	cfg := deleteByPrefixOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt.applyDeleteByPrefix(&cfg)
		}
	}
	if err := c.bumpPrefixMutationVersion(ctx, prefix); err != nil {
		return err
	}
	match := escapeRedisGlob(prefix) + "*"
	var cursor uint64
	iterations := 0
	for cfg.limit == 0 || iterations < cfg.limit {
		keys, nextCursor, err := c.redis.Scan(ctx, cursor, match, deleteByPrefixBatchSize).Result()
		if err != nil {
			return fmt.Errorf("cache delete by prefix scan: %w", err)
		}
		if len(keys) > 0 {
			if err := c.unlinkStaleValues(ctx, keys); err != nil {
				return err
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

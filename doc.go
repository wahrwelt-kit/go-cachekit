// Package cachekit provides Redis-backed and in-memory caching and key-value stores.
//
// # Redis connection
//
// RedisConfigFromURL parses redis:// and rediss:// URLs into RedisConfig. NewRedisClient builds a redis-go client from RedisConfig and verifies connectivity with Ping
// Host and Port are required; Username is supported for Redis ACL auth; DB must be non-negative; PoolSize defaults to 50 and MinIdleConns defaults to 10 capped by PoolSize when zero
// Do not log RedisConfig as-is; use String() or GoString() for safe output (password is redacted)
//
// # Redis JSON cache (Cache)
//
// New returns a Cache that uses the given *redis.Client. Optional CacheOptions (e.g. WithMaxVersionMapEntries) configure the cache. Values are serialized as JSON. Cache keys must be non-empty
//
//   - GetOrLoad[T]: package-level function (not a method) because Go has no generic methods on types. Call as GetOrLoad(c, ctx, key, ttl, loadFn, opts...). Returns the value for key from Redis, or runs loadFn(ctx), stores the result with ttl, and returns it
//     loadFn receives the request context and may respect context cancellation. Uses singleflight so concurrent
//     requests for the same key run loadFn once. Use each key with exactly one type T; using the same key with
//     different T causes a type error. ttl must be positive
//   - Set: writes value as JSON with the given ttl (must be positive). Also advances the key mutation fence so older in-flight GetOrLoad calls cannot overwrite it
//   - Del: deletes keys and advances each key mutation fence
//   - DeleteByPrefix: advances the prefix mutation fence, scans keys matching prefix*, and Unlinks them with their cache token keys. prefix must be non-empty. Pass WithDeleteByPrefixLimit(n) to cap SCAN iterations
//     Redis glob characters (\, *, ?, [, ]) in prefix are escaped
//
// GetOrLoad options: pass WithTimeout(d), WithRespectCallerCancel(true), and/or WithBypassOnCacheError(true) as variadic opts (type GetOrLoadOption). Resolved options are in GetOrLoadOpts
// Optional WithMaxVersionMapEntries(n) limits in-memory version map size; when exceeded, excess entries are evicted (no ordering guarantee)
// GetOrLoad stores a cache-entry token beside every JSON value and writes loaded values through a Redis Lua compare-and-set
// against exact-key and matching-prefix mutation fences. If Del, Set, or DeleteByPrefix runs while a load is in flight,
// that older load returns its data to the caller but does not write back to Redis. Calls that start after the mutation
// use a new singleflight generation. Prefix invalidation is logical immediately; physical deletion uses SCAN plus a Lua stale-token check so fresh post-invalidation values are not removed.
// If loadFn succeeds but Redis write-back fails, GetOrLoad returns (data, setErr) so the caller receives both the value and the cache write error. WithBypassOnCacheError(true) returns loaded data without surfacing Redis cache errors
// Values have TTL and will expire
//
// # In-memory SIEVE-style cache (SieveCache)
//
// NewSieveCache[K, V](maxSize) creates a cache using SIEVE-style second-chance eviction
// with at most maxSize entries (DefaultSieveCacheSize when maxSize <= 0).
// This cache uses lazy promotion and quick demotion of unpopular items. All operations are O(1) amortized
//
//   - Get marks the entry as visited (lazy promotion) so it survives eviction. Peek returns the value without
//     marking visited. Set inserts at the head or updates in place (marks visited). SetIfAbsent adds only if absent
//   - On eviction a hand pointer walks from tail to head: visited nodes have their bit cleared and are
//     skipped; the first unvisited node is evicted. This gives popular entries a second chance without
//     moving nodes on every access
//   - Delete removes an entry and adjusts the hand. Flush clears all entries. Len returns count; Cap returns maxSize
//
// Safe for concurrent use. Nil receiver is safe on all methods
//
// # Single-value TTL cache (CachedValue)
//
// CachedValue caches one value by key with TTL and singleflight. NewCachedValue returns an error if ttl <= 0 or key is empty. MustNewCachedValue panics on invalid input and is intended only for static configuration. It does not start background goroutines
// Get calls load with a configurable timeout (default 30s); use WithLoadTimeout(d) (CachedValueOption) when constructing to override. By default load ignores caller cancellation through context.WithoutCancel; pass WithRespectCallerCancel(true) to NewCachedValue when caller cancellation should abort the load. GetStale returns the in-TTL value or the last successfully loaded stale entry without calling load. Invalidate clears the value, stale entry, and singleflight; an in-flight Get that finishes after Invalidate will not write back
//
// # Key-value store
//
// KeyValueStore is a minimal Get/Set/Del interface. Get returns []byte; Set accepts []byte. RedisKeyValueStore implements it with *redis.Client. Set requires ttl > 0
// Methods return ErrRedisNotConfigured when Client is nil
//
// # Pub/Sub
//
// PubSubStore provides Publish and Subscribe. RedisPubSubStore implements it; channel names must be non-empty. ChanBufferSize is the subscribe channel buffer (default 64). SendTimeout (default 30s) limits how long the subscribe goroutine waits to send a message to the returned channel; when exceeded the message is dropped and OnDrop is invoked synchronously-keep OnDrop fast to avoid blocking the subscribe loop. OnError reports unexpected Redis-side subscription closure
// Subscribe's goroutine exits when ctx is cancelled. The caller must cancel ctx when done to avoid goroutine leaks. Correct usage
//
//	ctx, cancel := context.WithCancel(parent)
//	defer cancel()
//	ch, err := store.Subscribe(ctx, "mychannel")
//	if err != nil { ... }
//	for msg := range ch {
//	    handle(msg)
//	}
//
// # Errors
//
// ErrRedisNotConfigured is returned by Cache, RedisKeyValueStore, and RedisPubSubStore when the Redis client is nil
// ErrNotFound is returned by KeyValueStore.Get when the key does not exist
// ErrEmptyKey when key or keys are empty where non-empty is required
// ErrInvalidTTL when ttl is zero or negative
// ErrEmptyPrefix when prefix is empty for DeleteByPrefix
// ErrEmptyChannel when channel is empty for PubSubStore
// ErrNilContext when a required context is nil
// ErrNilLoadFunc when a required load function is nil
// ErrNilCachedValue when CachedValue.Get is called on nil receiver
// ErrUnexpectedType when cached value type does not match expected
// ErrRedisConfigNil, ErrRedisHostRequired, ErrRedisInvalidPort, ErrRedisInvalidDB, ErrRedisInvalidPoolSize, ErrRedisInvalidMinIdleConns by NewRedisClient on invalid config
// ErrRedisURLRequired and ErrRedisInvalidURL by RedisConfigFromURL
// ErrPubSubClosed may be reported through RedisPubSubStore.OnError
package cachekit

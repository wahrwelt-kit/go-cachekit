// Package cachekit provides Redis-backed and in-memory caching and key-value stores
//
// # Redis connection
//
// NewRedisClient builds a redis-go client from RedisConfig and verifies connectivity with Ping
// Host and Port are required; PoolSize and MinIdleConns default to 50 and 10 when zero
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
//   - Set: writes value as JSON with the given ttl (must be positive). Also forgets the key in singleflight and increments the per-key version so concurrent GetOrLoad does not use a stale singleflight result
//   - Del: deletes keys and forgets them in singleflight
//   - DeleteByPrefix: scans keys matching prefix*, Unlinks them, and forgets them in singleflight. prefix must be non-empty
//     Redis glob characters (\, *, ?, [, ]) in prefix are escaped
//
// GetOrLoad options: pass WithTimeout(d) and/or WithRespectCallerCancel(true) as variadic opts (type GetOrLoadOption). Resolved options are in GetOrLoadOpts
// Optional WithMaxVersionMapEntries(n) limits in-memory version map size; when exceeded, excess entries are evicted (no ordering guarantee)
// GetOrLoad caveats: Del and DeleteByPrefix increment a per-key version; a load that completes after the key was
// deleted will not write back to Redis. If loadFn succeeds but Redis Set fails, GetOrLoad returns (data, setErr)-caller
// gets both the data and the set error. Consistency is best-effort: there is a small race window
// between the in-memory version check and Redis Set; if Del runs in that window, a stale value may be written back
// Values have TTL and will expire
//
// # In-memory LRFU cache (LRFUCache)
//
// NewLRFUCache[K, V](maxSize) creates a cache using the LRFU (Lazy Recency/Frequency Update) eviction
// algorithm with at most maxSize entries (DefaultLRFUCacheSize when maxSize <= 0)
// LRFU achieves better hit rates than standard LRU on power-law/Zipf workloads by using
// lazy promotion and quick demotion of unpopular items. All operations are O(1)
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
// CachedValue caches one value by key with TTL and singleflight. Prefer NewCachedValueE (returns error) over NewCachedValue (panics if ttl <= 0 or key is empty) for config-driven or dynamic ttl. When ctx is cancelled the internal goroutine stops. If ctx is context.Background(), the goroutine never exits until Stop is called-call Stop when the value is no longer needed to avoid goroutine leaks
// Get calls load with a configurable timeout (default 30s); use WithLoadTimeout(d) (CachedValueOption) when constructing to override. GetStale returns the in-TTL value or the last successfully loaded stale entry without calling load. Invalidate clears the value, stale entry, and singleflight; an in-flight Get that finishes after Invalidate will not write back
//
// # Key-value store
//
// KeyValueStore is a minimal Get/Set/Del interface. Get returns []byte; Set accepts []byte. RedisKeyValueStore implements it with *redis.Client. Set requires ttl > 0
// Methods return ErrRedisNotConfigured when Client is nil
//
// # Pub/Sub
//
// PubSubStore provides Publish and Subscribe. RedisPubSubStore implements it; ChanBufferSize is the subscribe channel buffer (default 64). SendTimeout (default 30s) limits how long the subscribe goroutine waits to send a message to the returned channel; when exceeded the message is dropped and OnDrop is invoked synchronously-keep OnDrop fast to avoid blocking the subscribe loop
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
// ErrNilCachedValue when CachedValue.Get is called on nil receiver
// ErrUnexpectedType when cached value type does not match expected
// ErrRedisConfigNil, ErrRedisHostRequired, ErrRedisInvalidPort by NewRedisClient on invalid config
package cachekit

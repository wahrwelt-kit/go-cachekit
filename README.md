# go-cachekit

[![CI](https://github.com/wahrwelt-kit/go-cachekit/actions/workflows/ci.yml/badge.svg)](https://github.com/wahrwelt-kit/go-cachekit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wahrwelt-kit/go-cachekit.svg)](https://pkg.go.dev/github.com/wahrwelt-kit/go-cachekit)
[![Go Report Card](https://goreportcard.com/badge/github.com/wahrwelt-kit/go-cachekit)](https://goreportcard.com/report/github.com/wahrwelt-kit/go-cachekit)

Redis-backed JSON cache, in-memory SIEVE-style cache, TTL single-value cache, key-value and pub/sub helpers.

## Install

```bash
go get github.com/wahrwelt-kit/go-cachekit
```

```go
import "github.com/wahrwelt-kit/go-cachekit"
```

## API

### Cache (Redis JSON + singleflight)

- **New(client)** - build Cache from go-redis Client
- **GetOrLoad[T]** - get from Redis or call loadFn, store with ttl through a Lua exact/prefix mutation-fence check, return; singleflight per key and mutation generation; optional `WithBypassOnCacheError(true)` for cache-aside degradation
- **Del** - delete keys and advance each key fence
- **Set** - marshal value as JSON, set with ttl
- **DeleteByPrefix** - advance the prefix fence, scan prefix\*, unlink stale keys and cache tokens without deleting fresh post-fence writes

### SieveCache (in-memory SIEVE-style eviction)

- **NewSieveCache[K,V](maxSize)** - maxSize or DefaultSieveCacheSize (100) if ≤ 0
- **Get** - returns value and marks entry as visited (lazy promotion)
- **Peek** - returns value without marking visited
- **Set** - insert or update; evicts unvisited entries when full
- **SetIfAbsent** - insert only if key does not exist
- **Delete**, **Flush**, **Len**, **Cap**

SieveCache is intentionally the only in-memory eviction policy in this package. It keeps cache hits cheap by setting a visited bit instead of moving nodes, and it quickly removes one-off entries during eviction. That makes it a practical default for skewed workloads with a small hot set mixed with occasional scans.

### CachedValue (single key, TTL, singleflight)

- **NewCachedValue[T](key, ttl)** - one key, TTL + singleflight, returns error on invalid input
- **MustNewCachedValue[T](key, ttl)** - panic-on-error variant for static configuration
- **Get(ctx, load)** - cached or load(ctx), then cache
- **GetStale** - return in-TTL value or the last successfully loaded stale entry without loading
- **Invalidate** - delete, forget singleflight, and clear the stale entry
- **WithRespectCallerCancel(true)** - make load observe caller cancellation; default lets load finish and refresh the cache

### Redis client

- **RedisConfig** - Host, Port, Username, Password, PoolSize, MinIdleConns
- **RedisConfigFromURL(rawURL)** - parse `redis://` / `rediss://` into RedisConfig, including ACL username and `pool_size` / `min_idle_conns` query options
- **NewRedisClient(ctx, cfg)** - NewClient + Ping; fail-fast on invalid config or unreachable Redis

### Stores

- **KeyValueStore** - Get, Set, Del
- **RedisKeyValueStore** - implements KeyValueStore
- **PubSubStore** - Publish, Subscribe; channel names must be non-empty
- **RedisPubSubStore.OnDrop / OnError** - callbacks for dropped messages and unexpected subscription closure

## Example

```go
cfg, err := cachekit.RedisConfigFromURL("redis://cache-user:secret@localhost:6379/0?pool_size=32&min_idle_conns=8")
if err != nil {
    log.Fatal(err)
}

rdb, err := cachekit.NewRedisClient(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
defer rdb.Close()

c := cachekit.New(rdb)
val, err := cachekit.GetOrLoad(c, ctx, "user:1", 5*time.Minute, func(ctx context.Context) (User, error) {
    return db.GetUser(ctx, 1)
}, cachekit.WithBypassOnCacheError(true))

single, err := cachekit.NewCachedValue[User]("user:1:cached", time.Minute)
if err != nil {
    log.Fatal(err)
}

// SIEVE-style cache - lazy promotion and quick demotion for skewed workloads
sieve := cachekit.NewSieveCache[string, string](1000)
sieve.Set("k", "v")
if v, ok := sieve.Get("k"); ok {
    // v == "v", entry is now marked visited and survives eviction
}
```

## Attribution

SieveCache is inspired by the SIEVE cache eviction algorithm and the MIT-licensed Go implementation at [guerinoni/sieve](https://github.com/guerinoni/sieve). See [NOTICE](NOTICE).

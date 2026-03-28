# go-cachekit

[![CI](https://github.com/wahrwelt-kit/go-cachekit/actions/workflows/ci.yml/badge.svg)](https://github.com/wahrwelt-kit/go-cachekit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wahrwelt-kit/go-cachekit.svg)](https://pkg.go.dev/github.com/wahrwelt-kit/go-cachekit)
[![Go Report Card](https://goreportcard.com/badge/github.com/wahrwelt-kit/go-cachekit)](https://goreportcard.com/report/github.com/wahrwelt-kit/go-cachekit)

Redis-backed JSON cache, in-memory LRFU cache, TTL single-value cache, key-value and pub/sub helpers.

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
- **GetOrLoad[T]** - get from Redis or call loadFn, store with ttl, return; singleflight per key
- **Del** - delete keys and forget singleflight
- **Set** - marshal value as JSON, set with ttl
- **DeleteByPrefix** - scan prefix\*, unlink keys, forget singleflight

### LRFUCache (in-memory LRFU eviction)

- **NewLRFUCache[K,V](maxSize)** - maxSize or DefaultLRFUCacheSize (100) if ≤ 0
- **Get** - returns value and marks entry as visited (lazy promotion)
- **Peek** - returns value without marking visited
- **Set** - insert or update; evicts unvisited entries when full
- **SetIfAbsent** - insert only if key does not exist
- **Delete**, **Flush**, **Len**, **Cap**

### CachedValue (single key, TTL, singleflight)

- **NewCachedValue[T](key, ttl)** - one key, ttlcache + singleflight
- **Get(ctx, load)** - cached or load(ctx), then cache
- **GetStale** - return in-TTL value or the last successfully loaded stale entry without loading
- **Invalidate** - delete, forget singleflight, and clear the stale entry

### Redis client

- **RedisConfig** - Host, Port, Password, PoolSize, MinIdleConns
- **NewRedisClient(ctx, cfg)** - NewClient + Ping; error if unreachable

### Stores

- **KeyValueStore** - Get, Set, Del
- **RedisKeyValueStore** - implements KeyValueStore
- **PubSubStore** - Publish, Subscribe

## Example

```go
rdb, err := cachekit.NewRedisClient(ctx, &cachekit.RedisConfig{Host: "localhost", Port: 6379})
if err != nil {
    log.Fatal(err)
}
defer rdb.Close()

c := cachekit.New(rdb)
val, err := cachekit.GetOrLoad(c, ctx, "user:1", 5*time.Minute, func(ctx context.Context) (User, error) {
    return db.GetUser(ctx, 1)
})

// LRFU cache - better hit rate for skewed workloads than LRU
lrfu := cachekit.NewLRFUCache[string, string](1000)
lrfu.Set("k", "v")
if v, ok := lrfu.Get("k"); ok {
    // v == "v", entry is now marked visited and survives eviction
}
```

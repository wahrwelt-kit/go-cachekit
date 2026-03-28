package cachekit

import "errors"

var (
	// ErrRedisNotConfigured is returned by Cache, RedisKeyValueStore, and RedisPubSubStore when the Redis client is nil
	ErrRedisNotConfigured = errors.New("redis client not configured")
	// ErrNotFound is returned by KeyValueStore.Get when the key does not exist
	ErrNotFound = errors.New("cache: key not found")
	// ErrEmptyKey is returned when key or keys are empty where non-empty is required
	ErrEmptyKey = errors.New("cache: key must be non-empty")
	// ErrInvalidTTL is returned when ttl is zero or negative
	ErrInvalidTTL = errors.New("cache: ttl must be positive")
	// ErrEmptyPrefix is returned when prefix is empty for DeleteByPrefix
	ErrEmptyPrefix = errors.New("cache: empty prefix not allowed")
	// ErrNilCachedValue is returned when CachedValue.Get is called on nil receiver
	ErrNilCachedValue = errors.New("cache: CachedValue is nil")
	// ErrUnexpectedType is returned when cached value type does not match expected (e.g. type collision)
	ErrUnexpectedType = errors.New("cache: unexpected type")
	// ErrRedisConfigNil is returned by NewRedisClient when cfg is nil
	ErrRedisConfigNil = errors.New("redis config is nil")
	// ErrRedisHostRequired is returned by NewRedisClient when Host is empty
	ErrRedisHostRequired = errors.New("redis host is required")
	// ErrRedisInvalidPort is returned by NewRedisClient when Port is not in 1-65535
	ErrRedisInvalidPort = errors.New("redis port must be 1-65535")
)

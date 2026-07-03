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
	// ErrEmptyChannel is returned when a pub/sub channel name is empty
	ErrEmptyChannel = errors.New("cache: channel must be non-empty")
	// ErrNilContext is returned when a required context is nil
	ErrNilContext = errors.New("cache: context must be non-nil")
	// ErrNilLoadFunc is returned when a required load function is nil
	ErrNilLoadFunc = errors.New("cache: load function must be non-nil")
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
	// ErrRedisInvalidDB is returned by NewRedisClient when DB is negative
	ErrRedisInvalidDB = errors.New("redis db must be non-negative")
	// ErrRedisInvalidPoolSize is returned by NewRedisClient when PoolSize is negative
	ErrRedisInvalidPoolSize = errors.New("redis pool size must be non-negative")
	// ErrRedisInvalidMinIdleConns is returned by NewRedisClient when MinIdleConns is negative or greater than PoolSize
	ErrRedisInvalidMinIdleConns = errors.New("redis min idle conns must be non-negative and not exceed pool size")
	// ErrRedisURLRequired is returned by RedisConfigFromURL when URL is empty
	ErrRedisURLRequired = errors.New("redis url is required")
	// ErrRedisInvalidURL is returned by RedisConfigFromURL when URL cannot be parsed into RedisConfig
	ErrRedisInvalidURL = errors.New("redis url is invalid")
	// ErrPubSubClosed is reported to OnError when Redis closes a subscription channel unexpectedly
	ErrPubSubClosed = errors.New("cache: pubsub channel closed")
)

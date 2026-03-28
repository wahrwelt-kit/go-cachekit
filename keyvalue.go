package cachekit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyValueStore is a minimal key-value store with Get, Set, and Del
// Get and Set use []byte for symmetric serialized payloads (e.g. json.Marshal/Unmarshal). Set requires a positive TTL
type KeyValueStore interface {
	// Get returns the value for key as bytes, or an error if missing or the store is unavailable
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value at key with the given ttl. ttl must be positive. Value is raw bytes (e.g. JSON); no encoding is applied
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes the given keys. No-op if keys is empty
	Del(ctx context.Context, keys ...string) error
}

// RedisKeyValueStore implements KeyValueStore using a Redis client
// Client must be non-nil and must not be replaced after creation; otherwise methods return ErrRedisNotConfigured
type RedisKeyValueStore struct {
	// Client is the Redis client used for Get, Set, and Del. Must not be nil
	Client *redis.Client
}

var _ KeyValueStore = (*RedisKeyValueStore)(nil)

// Get returns the value for key from Redis as bytes. Returns ErrNotFound when the key does not exist, ErrRedisNotConfigured if Client is nil, ErrEmptyKey if key is empty
func (r *RedisKeyValueStore) Get(ctx context.Context, key string) ([]byte, error) {
	if r == nil || r.Client == nil {
		return nil, ErrRedisNotConfigured
	}
	if key == "" {
		return nil, ErrEmptyKey
	}
	val, err := r.Client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("keyvalue get: %w", err)
	}
	return val, nil
}

// Set writes value at key with the given ttl. ttl must be positive. Returns ErrRedisNotConfigured if Client is nil, ErrEmptyKey if key is empty, ErrInvalidTTL if ttl <= 0
func (r *RedisKeyValueStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if r == nil || r.Client == nil {
		return ErrRedisNotConfigured
	}
	if key == "" {
		return ErrEmptyKey
	}
	if ttl <= 0 {
		return fmt.Errorf("keyvalue set: %w, got %v", ErrInvalidTTL, ttl)
	}
	if err := r.Client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("keyvalue set: %w", err)
	}
	return nil
}

// Del removes the given keys from Redis. No-op if keys is empty. Returns ErrRedisNotConfigured if Client is nil, ErrEmptyKey if any key is empty
func (r *RedisKeyValueStore) Del(ctx context.Context, keys ...string) error {
	if r == nil || r.Client == nil {
		return ErrRedisNotConfigured
	}
	if len(keys) == 0 {
		return nil
	}
	if slices.Contains(keys, "") {
		return ErrEmptyKey
	}
	if err := r.Client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("keyvalue del: %w", err)
	}
	return nil
}

package cachekit

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds connection and pool settings for NewRedisClient
// Host and Port are required. Use String() or GoString() when logging to avoid exposing Password
type RedisConfig struct {
	// Host is the Redis server hostname or IP (required)
	Host string
	// Port is the Redis server port; must be in range 1-65535 (required)
	Port int
	// Password is the optional Redis auth password. Do not log; use GoString() for debug output
	Password string
	// DB is the Redis database number; 0 is the default database
	DB int
	// PoolSize is the maximum number of socket connections in the pool; zero uses default (50)
	PoolSize int
	// MinIdleConns is the minimum number of idle connections; zero uses default (10)
	MinIdleConns int
	// TLSConfig, when non-nil, enables TLS for the connection
	TLSConfig *tls.Config
}

// String implements fmt.Stringer. Returns a safe representation that does not include Password (use for %v, %s in logs)
func (c RedisConfig) String() string {
	return c.GoString()
}

// GoString implements fmt.GoStringer. Returns a safe representation that does not include Password (use for %#v in logs)
func (c RedisConfig) GoString() string {
	tlsStr := "nil"
	if c.TLSConfig != nil {
		tlsStr = "non-nil"
	}
	return fmt.Sprintf("RedisConfig{Host:%q, Port:%d, DB:%d, Password:\"***\", PoolSize:%d, MinIdleConns:%d, TLSConfig:%s}",
		c.Host, c.Port, c.DB, c.PoolSize, c.MinIdleConns, tlsStr)
}

const (
	redisDialTimeout  = 5 * time.Second
	redisReadTimeout  = 3 * time.Second
	redisWriteTimeout = 3 * time.Second
	redisPingTimeout  = 5 * time.Second
	redisDefaultPool  = 50
	redisDefaultIdle  = 10
)

// NewRedisClient creates a Redis client from cfg and verifies connectivity with Ping
// Returns the client on success. Returns an error if cfg is invalid (use ErrRedisConfigNil, ErrRedisHostRequired, ErrRedisInvalidPort) or if the server is unreachable. Ping uses the shorter of ctx's deadline and an internal timeout; ctx cancellation aborts the ping. Caller must call Close() on the returned client when done
func NewRedisClient(ctx context.Context, cfg *RedisConfig) (*redis.Client, error) {
	if cfg == nil {
		return nil, ErrRedisConfigNil
	}
	if cfg.Host == "" {
		return nil, ErrRedisHostRequired
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("%w, got %d", ErrRedisInvalidPort, cfg.Port)
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = redisDefaultPool
	}
	minIdle := cfg.MinIdleConns
	if minIdle <= 0 {
		minIdle = redisDefaultIdle
	}
	opts := &redis.Options{
		Addr:         cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  redisDialTimeout,
		ReadTimeout:  redisReadTimeout,
		WriteTimeout: redisWriteTimeout,
		PoolSize:     poolSize,
		MinIdleConns: minIdle,
	}
	if cfg.TLSConfig != nil {
		opts.TLSConfig = cfg.TLSConfig
	}
	rdb := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, redisPingTimeout)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		if closeErr := rdb.Close(); closeErr != nil {
			return nil, fmt.Errorf("redis connection failed: %w (close: %w)", err, closeErr)
		}
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return rdb, nil
}

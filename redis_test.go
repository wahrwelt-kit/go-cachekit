package cachekit

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHostLocalhost = "localhost"

func TestNewRedisClient_Error(t *testing.T) {
	t.Parallel()
	_, err := NewRedisClient(context.Background(), &RedisConfig{Host: "127.0.0.1", Port: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "redis connection failed")
}

func TestNewRedisClient_NilConfig(t *testing.T) {
	t.Parallel()
	_, err := NewRedisClient(context.Background(), nil)
	require.ErrorIs(t, err, ErrRedisConfigNil)
}

func TestNewRedisClient_NilContext(t *testing.T) {
	t.Parallel()
	_, err := NewRedisClient(nilContext(), &RedisConfig{Host: testHostLocalhost, Port: 6379})
	require.ErrorIs(t, err, ErrNilContext)
}

func TestNewRedisClient_EmptyHost(t *testing.T) {
	t.Parallel()
	_, err := NewRedisClient(context.Background(), &RedisConfig{Port: 6379})
	require.ErrorIs(t, err, ErrRedisHostRequired)
}

func TestNewRedisClient_InvalidPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRedisClient(context.Background(), &RedisConfig{Host: testHostLocalhost, Port: tt.port})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRedisInvalidPort)
		})
	}
}

func TestNewRedisClient_InvalidDB(t *testing.T) {
	t.Parallel()
	_, err := NewRedisClient(context.Background(), &RedisConfig{Host: testHostLocalhost, Port: 6379, DB: -1})
	require.ErrorIs(t, err, ErrRedisInvalidDB)
}

func TestRedisConfigFromURL(t *testing.T) {
	t.Parallel()
	cfg, err := RedisConfigFromURL("redis://cache-user:secret@localhost:6380/2?pool_size=32&min_idle_conns=7")
	require.NoError(t, err)
	require.Equal(t, testHostLocalhost, cfg.Host)
	require.Equal(t, 6380, cfg.Port)
	require.Equal(t, "cache-user", cfg.Username)
	require.Equal(t, "secret", cfg.Password)
	require.Equal(t, 2, cfg.DB)
	require.Equal(t, 32, cfg.PoolSize)
	require.Equal(t, 7, cfg.MinIdleConns)
	require.Nil(t, cfg.TLSConfig)
}

func TestRedisConfigFromURL_Rediss(t *testing.T) {
	t.Parallel()
	cfg, err := RedisConfigFromURL("rediss://localhost:6380/0")
	require.NoError(t, err)
	require.Equal(t, testHostLocalhost, cfg.Host)
	require.Equal(t, 6380, cfg.Port)
	require.NotNil(t, cfg.TLSConfig)
}

func TestRedisConfigFromURL_Invalid(t *testing.T) {
	t.Parallel()
	_, err := RedisConfigFromURL("")
	require.ErrorIs(t, err, ErrRedisURLRequired)
	_, err = RedisConfigFromURL("://not-a-url")
	require.ErrorIs(t, err, ErrRedisInvalidURL)
	_, err = RedisConfigFromURL("redis://localhost:6379/0?pool_size=invalid")
	require.ErrorIs(t, err, ErrRedisInvalidURL)
}

func TestNewRedisClient_InvalidPoolSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  RedisConfig
		want error
	}{
		{
			name: "negative pool size",
			cfg:  RedisConfig{Host: testHostLocalhost, Port: 6379, PoolSize: -1},
			want: ErrRedisInvalidPoolSize,
		},
		{
			name: "negative min idle",
			cfg:  RedisConfig{Host: testHostLocalhost, Port: 6379, MinIdleConns: -1},
			want: ErrRedisInvalidMinIdleConns,
		},
		{
			name: "min idle greater than pool size",
			cfg:  RedisConfig{Host: testHostLocalhost, Port: 6379, PoolSize: 2, MinIdleConns: 3},
			want: ErrRedisInvalidMinIdleConns,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRedisClient(context.Background(), &tt.cfg)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestRedisConfig_String(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: testHostLocalhost, Port: 6379, Username: "cache-user", Password: "secret", DB: 0}
	s := cfg.String()
	assert.Contains(t, s, testHostLocalhost)
	assert.Contains(t, s, "cache-user")
	assert.Contains(t, s, "***")
	assert.NotContains(t, s, "secret")
}

func TestRedisConfig_GoString(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: testHostLocalhost, Port: 6379, Username: "cache-user", Password: "secret", DB: 0}
	s := cfg.GoString()
	assert.Contains(t, s, testHostLocalhost)
	assert.Contains(t, s, "cache-user")
	assert.Contains(t, s, "***")
	assert.NotContains(t, s, "secret")
}

func TestRedisConfig_GoString_WithTLS(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: testHostLocalhost, Port: 6379, TLSConfig: &tls.Config{}}
	s := cfg.GoString()
	assert.Contains(t, s, "non-nil")
}

func TestRedisConfig_GoString_NilTLS(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: testHostLocalhost, Port: 6379}
	s := cfg.GoString()
	assert.Contains(t, s, "nil")
}

package cachekit

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			_, err := NewRedisClient(context.Background(), &RedisConfig{Host: "localhost", Port: tt.port})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRedisInvalidPort)
		})
	}
}

func TestRedisConfig_String(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: "localhost", Port: 6379, Password: "secret", DB: 0}
	s := cfg.String()
	assert.Contains(t, s, "localhost")
	assert.Contains(t, s, "***")
	assert.NotContains(t, s, "secret")
}

func TestRedisConfig_GoString(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: "localhost", Port: 6379, Password: "secret", DB: 0}
	s := cfg.GoString()
	assert.Contains(t, s, "localhost")
	assert.Contains(t, s, "***")
	assert.NotContains(t, s, "secret")
}

func TestRedisConfig_GoString_WithTLS(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: "localhost", Port: 6379, TLSConfig: &tls.Config{}}
	s := cfg.GoString()
	assert.Contains(t, s, "non-nil")
}

func TestRedisConfig_GoString_NilTLS(t *testing.T) {
	t.Parallel()
	cfg := RedisConfig{Host: "localhost", Port: 6379}
	s := cfg.GoString()
	assert.Contains(t, s, "nil")
}

//go:build integration

package cachekit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	endpoint, err := container.Endpoint(ctx, "")
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: endpoint})
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(ctx).Err())
	return client
}

func TestIntegration_NewRedisClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)

	client, err := NewRedisClient(ctx, &RedisConfig{
		Host:     host,
		Port:     int(port.Num()),
		PoolSize: 5,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(ctx).Err())
}

func TestIntegration_Cache_GetOrLoad(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	c := New(rc)
	ctx := context.Background()

	val, err := GetOrLoad(c, ctx, "test:key", time.Minute, func(ctx context.Context) (map[string]int, error) {
		return map[string]int{"a": 1}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"a": 1}, val)

	val, err = GetOrLoad(c, ctx, "test:key", time.Minute, func(ctx context.Context) (map[string]int, error) {
		t.Error("load should not be called on cache hit")
		return nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"a": 1}, val)
}

func TestIntegration_Cache_GetOrLoad_WithOptions(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	c := New(rc)
	ctx := context.Background()

	val, err := GetOrLoad(c, ctx, "test:opts", time.Minute, func(ctx context.Context) (int, error) {
		return 42, nil
	}, WithTimeout(5*time.Second), WithRespectCallerCancel(true))
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestIntegration_Cache_SetAndDel(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	c := New(rc)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "test:set", "hello", time.Minute))

	raw, err := rc.Get(ctx, "test:set").Result()
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, raw)

	require.NoError(t, c.Del(ctx, "test:set"))

	_, err = rc.Get(ctx, "test:set").Result()
	assert.ErrorIs(t, err, redis.Nil)
}

func TestIntegration_Cache_DeleteByPrefix(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	c := New(rc)
	ctx := context.Background()

	for i := range 10 {
		require.NoError(t, c.Set(ctx, fmt.Sprintf("pfx:%d", i), i, time.Minute))
	}
	require.NoError(t, c.Set(ctx, "other:key", "x", time.Minute))

	require.NoError(t, c.DeleteByPrefix(ctx, "pfx:"))

	keys, err := rc.Keys(ctx, "pfx:*").Result()
	require.NoError(t, err)
	assert.Empty(t, keys)

	val, err := rc.Get(ctx, "other:key").Result()
	require.NoError(t, err)
	assert.Equal(t, `"x"`, val)
}

func TestIntegration_Cache_WithMaxVersionMapEntries(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	c := New(rc, WithMaxVersionMapEntries(5))
	ctx := context.Background()

	for i := range 20 {
		require.NoError(t, c.Set(ctx, fmt.Sprintf("evict:%d", i), i, time.Minute))
	}
	assert.LessOrEqual(t, c.versionMapSize.Load(), int64(6))
}

func TestIntegration_KeyValueStore_CRUD(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	store := &RedisKeyValueStore{Client: rc}
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "kv:key", []byte("value"), time.Minute))

	got, err := store.Get(ctx, "kv:key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), got)

	require.NoError(t, store.Del(ctx, "kv:key"))

	_, err = store.Get(ctx, "kv:key")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIntegration_KeyValueStore_Del_MultipleKeys(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	store := &RedisKeyValueStore{Client: rc}
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "kv:a", []byte("1"), time.Minute))
	require.NoError(t, store.Set(ctx, "kv:b", []byte("2"), time.Minute))

	require.NoError(t, store.Del(ctx, "kv:a", "kv:b"))

	_, err := store.Get(ctx, "kv:a")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.Get(ctx, "kv:b")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIntegration_PubSub_PublishSubscribe(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	ps := &RedisPubSubStore{Client: rc, ChanBufferSize: 8}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, "test:pubsub")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, ps.Publish(context.Background(), "test:pubsub", "hello"))

	select {
	case msg := <-ch:
		assert.Equal(t, "hello", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestIntegration_PubSub_MultipleMessages(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	ps := &RedisPubSubStore{Client: rc, ChanBufferSize: 16}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, "test:multi")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	for i := range 5 {
		require.NoError(t, ps.Publish(context.Background(), "test:multi", fmt.Sprintf("msg-%d", i)))
	}

	received := make([]string, 0, 5)
	for range 5 {
		select {
		case msg := <-ch:
			received = append(received, msg)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout, received only %d messages", len(received))
		}
	}
	assert.Len(t, received, 5)
}

func TestIntegration_PubSub_ContextCancel(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	ps := &RedisPubSubStore{Client: rc}
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := ps.Subscribe(ctx, "test:cancel")
	require.NoError(t, err)

	cancel()

	for range ch {
	}
}

func TestIntegration_PubSub_OnDrop(t *testing.T) {
	t.Parallel()
	rc := startRedis(t)
	dropped := make(chan string, 10)
	ps := &RedisPubSubStore{
		Client:         rc,
		ChanBufferSize: 1,
		SendTimeout:    50 * time.Millisecond,
		OnDrop: func(channel, payload string) {
			dropped <- payload
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, "test:drop")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	for i := range 20 {
		_ = ps.Publish(context.Background(), "test:drop", fmt.Sprintf("msg-%d", i))
	}

	time.Sleep(2 * time.Second)

	select {
	case p := <-dropped:
		assert.NotEmpty(t, p)
	default:
	}

	for len(ch) > 0 {
		<-ch
	}
}

package cachekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrLoad_CacheHit(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()

	data := map[string]int{"x": 1}
	bytes, _ := json.Marshal(data)
	mock.ExpectGet("key").SetVal(string(bytes))

	loadCalled := false
	val, err := GetOrLoad(c, ctx, "key", time.Minute, func(context.Context) (map[string]int, error) {
		loadCalled = true
		return nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, data, val)
	assert.False(t, loadCalled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_CacheMiss(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()

	mock.ExpectGet("key").SetErr(redis.Nil)
	mock.ExpectSet("key", []byte(`{"x":2}`), time.Minute).SetVal("OK")

	val, err := GetOrLoad(c, ctx, "key", time.Minute, func(context.Context) (map[string]int, error) {
		return map[string]int{"x": 2}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"x": 2}, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDel(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()

	mock.ExpectDel("k1", "k2").SetVal(2)
	err := c.Del(ctx, "k1", "k2")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSet(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()

	mock.ExpectSet("k", []byte(`{"N":10}`), time.Second).SetVal("OK")
	err := c.Set(ctx, "k", struct{ N int }{10}, time.Second)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByPrefix(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()

	mock.ExpectScan(0, "p*", 500).SetVal([]string{"p1", "p2"}, 0)
	mock.ExpectUnlink("p1", "p2").SetVal(2)
	err := c.DeleteByPrefix(ctx, "p")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_EmptyKey(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()
	_, err := GetOrLoad(c, ctx, "", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestGetOrLoad_ZeroTTL(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()
	_, err := GetOrLoad(c, ctx, "k", 0, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestGetOrLoad_LoadError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("k").SetErr(redis.Nil)
	c := New(client)
	ctx := context.Background()
	loadErr := errors.New("load failed")
	_, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) {
		return 0, loadErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
}

func TestGetOrLoad_RedisSetError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("k").SetErr(redis.Nil)
	mock.ExpectSet("k", []byte(`42`), time.Minute).SetErr(errors.New("redis set failed"))
	c := New(client)
	ctx := context.Background()
	val, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) { return 42, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set after load")
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_SingleflightDedup(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("k").SetErr(redis.Nil)
	mock.ExpectSet("k", []byte(`1`), time.Minute).SetVal("OK")
	mock.ExpectGet("k").SetVal("1")
	c := New(client)
	ctx := context.Background()
	var loadCalls atomic.Int32
	loadFn := func(context.Context) (int, error) {
		loadCalls.Add(1)
		return 1, nil
	}
	v1, err := GetOrLoad(c, ctx, "k", time.Minute, loadFn)
	require.NoError(t, err)
	assert.Equal(t, 1, v1)
	assert.Equal(t, int32(1), loadCalls.Load())
	v2, err := GetOrLoad(c, ctx, "k", time.Minute, loadFn)
	require.NoError(t, err)
	assert.Equal(t, 1, v2)
	assert.Equal(t, int32(1), loadCalls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_UnmarshalError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("k").SetVal("not valid json")
	mock.ExpectDel("k").SetVal(1)
	c := New(client)
	ctx := context.Background()
	_, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (map[string]int, error) {
		return nil, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestSet_EmptyKey(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()
	err := c.Set(ctx, "", 1, time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestSet_ZeroTTL(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()
	err := c.Set(ctx, "k", 1, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestDeleteByPrefix_EmptyPrefix(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	ctx := context.Background()
	err := c.DeleteByPrefix(ctx, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyPrefix)
}

func TestNew_NilOption(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client, nil)
	assert.NotNil(t, c)
	assert.Equal(t, defaultMaxVersionMapEntries, c.maxVersionMapEntries)
}

func TestNew_WithMaxVersionMapEntries(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client, WithMaxVersionMapEntries(10))
	assert.Equal(t, 10, c.maxVersionMapEntries)
}

func TestGetOrLoad_NilCache(t *testing.T) {
	t.Parallel()
	_, err := GetOrLoad(nil, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestGetOrLoad_NilRedis(t *testing.T) {
	t.Parallel()
	c := &Cache{}
	_, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestGetOrLoad_WithTimeout(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectGet("k").SetErr(redis.Nil)
	mock.ExpectSet("k", []byte(`1`), time.Minute).SetVal("OK")
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 1, nil }, WithTimeout(5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_WithRespectCallerCancel(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectGet("k").SetErr(redis.Nil)
	mock.ExpectSet("k", []byte(`1`), time.Minute).SetVal("OK")
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 1, nil }, WithRespectCallerCancel(true))
	require.NoError(t, err)
	assert.Equal(t, 1, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_RedisGetError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectGet("k").SetErr(errors.New("connection refused"))
	_, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache get")
}

func TestGetOrLoad_VersionBumpSkipsRedisSet(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectGet("k").SetErr(redis.Nil)
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) {
		cacheKeyVersion(c, "k").Add(1)
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_UnmarshalError_DelFails(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectGet("k").SetVal("not valid json")
	mock.ExpectDel("k").SetErr(errors.New("del failed"))
	_, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (map[string]int, error) {
		return nil, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
	assert.Contains(t, err.Error(), "del failed")
}

func TestDel_NilCache(t *testing.T) {
	t.Parallel()
	var c *Cache
	err := c.Del(context.Background(), "k")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestDel_EmptyKeys(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	require.NoError(t, c.Del(context.Background()))
}

func TestDel_EmptyKeyString(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	require.ErrorIs(t, c.Del(context.Background(), ""), ErrEmptyKey)
}

func TestDel_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectDel("k").SetErr(errors.New("redis down"))
	require.Error(t, c.Del(context.Background(), "k"))
}

func TestSet_NilCache(t *testing.T) {
	t.Parallel()
	var c *Cache
	require.ErrorIs(t, c.Set(context.Background(), "k", 1, time.Second), ErrRedisNotConfigured)
}

func TestSet_MarshalError(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client)
	err := c.Set(context.Background(), "k", make(chan int), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestSet_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectSet("k", []byte(`1`), time.Second).SetErr(errors.New("redis down"))
	err := c.Set(context.Background(), "k", 1, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache set")
}

func TestDeleteByPrefix_NilCache(t *testing.T) {
	t.Parallel()
	var c *Cache
	require.ErrorIs(t, c.DeleteByPrefix(context.Background(), "p"), ErrRedisNotConfigured)
}

func TestDeleteByPrefix_ScanError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectScan(0, "p*", 500).SetErr(errors.New("scan failed"))
	err := c.DeleteByPrefix(context.Background(), "p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan")
}

func TestDeleteByPrefix_UnlinkError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	c := New(client)
	mock.ExpectScan(0, "p*", 500).SetVal([]string{"p1"}, 0)
	mock.ExpectUnlink("p1").SetErr(errors.New("unlink failed"))
	err := c.DeleteByPrefix(context.Background(), "p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unlink")
}

func TestEvictVersionMapExcess(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	c := New(client, WithMaxVersionMapEntries(3))
	for i := range 10 {
		cacheKeyVersion(c, fmt.Sprintf("key-%d", i))
	}
	assert.LessOrEqual(t, c.versionMapSize.Load(), int64(4))
}

func TestDeleteByPrefix_MaxIterations(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectScan(0, "x*", 500).SetVal([]string{"x1"}, 1)
	mock.ExpectUnlink("x1").SetVal(1)
	mock.ExpectScan(1, "x*", 500).SetVal([]string{"x2"}, 2)
	mock.ExpectUnlink("x2").SetVal(1)
	mock.ExpectScan(2, "x*", 500).SetVal([]string{"x3"}, 0)
	mock.ExpectUnlink("x3").SetVal(1)
	c := New(client)
	ctx := context.Background()
	err := c.DeleteByPrefix(ctx, "x", 3)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

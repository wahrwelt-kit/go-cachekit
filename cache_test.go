package cachekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMutationToken = "k\x1f0"

func expectMutationSnapshot(mock *redisMock, key string) {
	mock.ExpectScript(mutationSnapshotScript, snapshotKeys(key), mutationScriptArgs(key)...).SetVal(testMutationToken)
}

func expectFreshValue(mock *redisMock, key, value string) {
	mock.ExpectScript(getFreshValueScript, valueScriptKeys(key), mutationScriptArgs(key)...).SetVal([]any{int64(1), value})
}

func expectFreshValueMiss(mock *redisMock, key string) *redisMockExpectation {
	return mock.ExpectScript(getFreshValueScript, valueScriptKeys(key), mutationScriptArgs(key)...).SetVal([]any{int64(0)})
}

func expectLoadedValueWrite(mock *redisMock, key string, value []byte) *redisMockExpectation {
	return mock.ExpectScript(setLoadedValueScript, valueScriptKeys(key), mutationScriptArgs(key, testMutationToken, value, ttlMilliseconds(time.Minute))...)
}

func expectSetValue(mock *redisMock, key string, value []byte, ttl time.Duration) *redisMockExpectation {
	return mock.ExpectScript(setValueScript, valueScriptKeys(key), mutationScriptArgs(key, value, ttlMilliseconds(ttl))...)
}

func expectBumpPrefix(mock *redisMock, prefix string) *redisMockExpectation {
	return mock.ExpectScript(bumpPrefixFenceScript, []string{prefixFenceKey(prefix)})
}

func expectDelKeys(mock *redisMock, keys ...string) *redisMockExpectation {
	return mock.ExpectScript(delKeysScript, delScriptKeys(keys), len(keys))
}

func expectUnlinkStaleValues(mock *redisMock, keys ...string) *redisMockExpectation {
	return mock.ExpectScript(unlinkStaleValuesScript, delScriptKeys(keys), len(keys), redisPrefixFencePrefix)
}

func TestGetOrLoad_CacheHit(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()

	data := map[string]int{"x": 1}
	bytes, _ := json.Marshal(data)
	expectFreshValue(mock, "key", string(bytes))

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
	client, mock := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()

	expectFreshValueMiss(mock, "key")
	expectMutationSnapshot(mock, "key")
	expectLoadedValueWrite(mock, "key", []byte(`{"x":2}`)).SetVal(1)

	val, err := GetOrLoad(c, ctx, "key", time.Minute, func(context.Context) (map[string]int, error) {
		return map[string]int{"x": 2}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"x": 2}, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDel(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()

	expectDelKeys(mock, "k1", "k2").SetVal(4)
	err := c.Del(ctx, "k1", "k2")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSet(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()

	expectSetValue(mock, "k", []byte(`{"N":10}`), time.Second).SetVal(1)
	err := c.Set(ctx, "k", struct{ N int }{10}, time.Second)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByPrefix(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()

	expectBumpPrefix(mock, "p").SetVal(1)
	mock.ExpectScan(0, "p*", 500).SetVal([]string{"p1", "p2"}, 0)
	expectUnlinkStaleValues(mock, "p1", "p2").SetVal(4)
	err := c.DeleteByPrefix(ctx, "p")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_EmptyKey(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()
	_, err := GetOrLoad(c, ctx, "", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestGetOrLoad_NilContext(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	_, err := GetOrLoad(c, nilContext(), "k", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.ErrorIs(t, err, ErrNilContext)
}

func TestGetOrLoad_NilLoadFunc(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	_, err := GetOrLoad[int](c, context.Background(), "k", time.Minute, nil)
	require.ErrorIs(t, err, ErrNilLoadFunc)
}

func TestGetOrLoad_ZeroTTL(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()
	_, err := GetOrLoad(c, ctx, "k", 0, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestGetOrLoad_LoadError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k")
	c := New(client)
	ctx := context.Background()
	loadErr := errors.New("load failed")
	expectMutationSnapshot(mock, "k")
	_, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) {
		return 0, loadErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, loadErr)
}

func TestGetOrLoad_RedisSetError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`42`)).SetErr(errors.New("redis set failed"))
	c := New(client)
	ctx := context.Background()
	val, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) { return 42, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set after load")
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_BypassOnCacheReadError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k").SetErr(errors.New("redis down"))
	c := New(client)
	ctx := context.Background()
	val, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) { return 42, nil }, WithBypassOnCacheError(true))
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_BypassOnMutationSnapshotError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k")
	mock.ExpectScript(mutationSnapshotScript, snapshotKeys("k"), mutationScriptArgs("k")...).SetErr(errors.New("redis down"))
	c := New(client)
	ctx := context.Background()
	val, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) { return 42, nil }, WithBypassOnCacheError(true))
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_BypassOnCacheWriteError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`42`)).SetErr(errors.New("redis down"))
	c := New(client)
	ctx := context.Background()
	val, err := GetOrLoad(c, ctx, "k", time.Minute, func(context.Context) (int, error) { return 42, nil }, WithBypassOnCacheError(true))
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_SingleflightDedup(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`1`)).SetVal(1)
	expectFreshValue(mock, "k", "1")
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
	client, mock := newRedisClientMock(t)
	expectFreshValue(mock, "k", "not valid json")
	expectDelKeys(mock, "k").SetVal(2)
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
	client, _ := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()
	err := c.Set(ctx, "", 1, time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyKey)
}

func TestSet_ZeroTTL(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()
	err := c.Set(ctx, "k", 1, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestDeleteByPrefix_EmptyPrefix(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	ctx := context.Background()
	err := c.DeleteByPrefix(ctx, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyPrefix)
}

func TestNew_NilOption(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client, nil)
	assert.NotNil(t, c)
	assert.Equal(t, defaultMaxVersionMapEntries, c.maxVersionMapEntries)
}

func TestNew_WithMaxVersionMapEntries(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
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
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`1`)).SetVal(1)
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 1, nil }, WithTimeout(5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_WithRespectCallerCancel(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`1`)).SetVal(1)
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 1, nil }, WithRespectCallerCancel(true))
	require.NoError(t, err)
	assert.Equal(t, 1, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrLoad_RedisGetError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k").SetErr(errors.New("connection refused"))
	_, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) { return 0, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache get")
}

func TestGetOrLoad_StaleEntryTokenReloads(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	expectLoadedValueWrite(mock, "k", []byte(`2`)).SetVal(1)
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, val)
}

func TestGetOrLoad_CacheReadScriptError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k").SetErr(errors.New("redis down"))
	_, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) {
		t.Fatal("load function must not be called when cache read fails")
		return 0, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache get")
}

func TestGetOrLoad_VersionBumpSkipsRedisSet(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValueMiss(mock, "k")
	expectMutationSnapshot(mock, "k")
	val, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (int, error) {
		cacheKeyVersion(c, "k").Add(1)
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestGetOrLoad_UnmarshalError_DelFails(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectFreshValue(mock, "k", "not valid json")
	expectDelKeys(mock, "k").SetErr(errors.New("del failed"))
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
	client, _ := newRedisClientMock(t)
	c := New(client)
	require.NoError(t, c.Del(context.Background()))
}

func TestDel_NilContext(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	require.ErrorIs(t, c.Del(nilContext(), "k"), ErrNilContext)
}

func TestDel_EmptyKeyString(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	require.ErrorIs(t, c.Del(context.Background(), ""), ErrEmptyKey)
}

func TestDel_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectDelKeys(mock, "k").SetErr(errors.New("redis down"))
	require.Error(t, c.Del(context.Background(), "k"))
}

func TestSet_NilCache(t *testing.T) {
	t.Parallel()
	var c *Cache
	require.ErrorIs(t, c.Set(context.Background(), "k", 1, time.Second), ErrRedisNotConfigured)
}

func TestSet_NilContext(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	require.ErrorIs(t, c.Set(nilContext(), "k", 1, time.Second), ErrNilContext)
}

func TestSet_MarshalError(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	err := c.Set(context.Background(), "k", make(chan int), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestSet_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectSetValue(mock, "k", []byte(`1`), time.Second).SetErr(errors.New("redis down"))
	err := c.Set(context.Background(), "k", 1, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache set")
}

func TestDeleteByPrefix_NilCache(t *testing.T) {
	t.Parallel()
	var c *Cache
	require.ErrorIs(t, c.DeleteByPrefix(context.Background(), "p"), ErrRedisNotConfigured)
}

func TestDeleteByPrefix_NilContext(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client)
	require.ErrorIs(t, c.DeleteByPrefix(nilContext(), "p"), ErrNilContext)
}

func TestDeleteByPrefix_ScanError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectBumpPrefix(mock, "p").SetVal(1)
	mock.ExpectScan(0, "p*", 500).SetErr(errors.New("scan failed"))
	err := c.DeleteByPrefix(context.Background(), "p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan")
}

func TestDeleteByPrefix_UnlinkError(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	c := New(client)
	expectBumpPrefix(mock, "p").SetVal(1)
	mock.ExpectScan(0, "p*", 500).SetVal([]string{"p1"}, 0)
	expectUnlinkStaleValues(mock, "p1").SetErr(errors.New("unlink failed"))
	err := c.DeleteByPrefix(context.Background(), "p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unlink")
}

func TestEvictVersionMapExcess(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	c := New(client, WithMaxVersionMapEntries(3))
	for i := range 10 {
		cacheKeyVersion(c, fmt.Sprintf("key-%d", i))
	}
	assert.LessOrEqual(t, c.versionMapSize.Load(), int64(4))
}

func TestDeleteByPrefix_MaxIterations(t *testing.T) {
	t.Parallel()
	client, mock := newRedisClientMock(t)
	expectBumpPrefix(mock, "x").SetVal(1)
	mock.ExpectScan(0, "x*", 500).SetVal([]string{"x1"}, 1)
	expectUnlinkStaleValues(mock, "x1").SetVal(2)
	mock.ExpectScan(1, "x*", 500).SetVal([]string{"x2"}, 2)
	expectUnlinkStaleValues(mock, "x2").SetVal(2)
	mock.ExpectScan(2, "x*", 500).SetVal([]string{"x3"}, 0)
	expectUnlinkStaleValues(mock, "x3").SetVal(2)
	c := New(client)
	ctx := context.Background()
	err := c.DeleteByPrefix(ctx, "x", WithDeleteByPrefixLimit(3))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

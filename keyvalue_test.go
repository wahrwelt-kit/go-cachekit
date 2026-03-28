package cachekit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisKeyValueStore_NilClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &RedisKeyValueStore{}
	val, err := s.Get(ctx, "k")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
	assert.Empty(t, val)

	err = s.Set(ctx, "k", []byte("v"), time.Minute)
	require.ErrorIs(t, err, ErrRedisNotConfigured)

	err = s.Del(ctx, "k")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestRedisKeyValueStore_NilReceiver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var s *RedisKeyValueStore
	_, err := s.Get(ctx, "k")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
	err = s.Set(ctx, "k", []byte("v"), time.Minute)
	require.ErrorIs(t, err, ErrRedisNotConfigured)
	err = s.Del(ctx, "k")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestRedisKeyValueStore_Get_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("missing").SetErr(redis.Nil)
	store := &RedisKeyValueStore{Client: client}
	val, err := store.Get(context.Background(), "missing")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisKeyValueStore_SetGet_RoundTrip(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	payload := []byte(`{"x":1}`)
	mock.ExpectSet("k", payload, time.Minute).SetVal("OK")
	mock.ExpectGet("k").SetVal(string(payload))
	store := &RedisKeyValueStore{Client: client}
	ctx := context.Background()
	err := store.Set(ctx, "k", payload, time.Minute)
	require.NoError(t, err)
	got, err := store.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisKeyValueStore_Get_EmptyKey(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	store := &RedisKeyValueStore{Client: client}
	_, err := store.Get(context.Background(), "")
	require.ErrorIs(t, err, ErrEmptyKey)
}

func TestRedisKeyValueStore_Set_EmptyKey(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	store := &RedisKeyValueStore{Client: client}
	err := store.Set(context.Background(), "", []byte("v"), time.Minute)
	require.ErrorIs(t, err, ErrEmptyKey)
}

func TestRedisKeyValueStore_Set_InvalidTTL(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	store := &RedisKeyValueStore{Client: client}
	err := store.Set(context.Background(), "k", []byte("v"), 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestRedisKeyValueStore_Del_NoKeys(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	store := &RedisKeyValueStore{Client: client}
	require.NoError(t, store.Del(context.Background()))
}

func TestRedisKeyValueStore_Del_EmptyKeyString(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	store := &RedisKeyValueStore{Client: client}
	require.ErrorIs(t, store.Del(context.Background(), ""), ErrEmptyKey)
}

func TestRedisKeyValueStore_Get_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("k").SetErr(errors.New("redis down"))
	store := &RedisKeyValueStore{Client: client}
	_, err := store.Get(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyvalue get")
}

func TestRedisKeyValueStore_Set_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectSet("k", []byte("v"), time.Minute).SetErr(errors.New("redis down"))
	store := &RedisKeyValueStore{Client: client}
	err := store.Set(context.Background(), "k", []byte("v"), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyvalue set")
}

func TestRedisKeyValueStore_Del_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	mock.ExpectDel("k").SetErr(errors.New("redis down"))
	store := &RedisKeyValueStore{Client: client}
	err := store.Del(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyvalue del")
}

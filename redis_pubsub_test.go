package cachekit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisPubSubStore_NilClient_Publish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &RedisPubSubStore{}
	err := s.Publish(ctx, "ch", "msg")
	assert.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestRedisPubSubStore_NilClient_Subscribe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &RedisPubSubStore{}
	ch, err := s.Subscribe(ctx, "ch")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
	assert.Nil(t, ch)
}

func TestRedisPubSubStore_NilReceiver_Publish(t *testing.T) {
	t.Parallel()
	var s *RedisPubSubStore
	err := s.Publish(context.Background(), "ch", "msg")
	assert.ErrorIs(t, err, ErrRedisNotConfigured)
}

func TestRedisPubSubStore_NilReceiver_Subscribe(t *testing.T) {
	t.Parallel()
	var s *RedisPubSubStore
	ch, err := s.Subscribe(context.Background(), "ch")
	require.ErrorIs(t, err, ErrRedisNotConfigured)
	assert.Nil(t, ch)
}

func TestRedisPubSubStore_EmptyChannel(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	s := &RedisPubSubStore{Client: client}
	require.ErrorIs(t, s.Publish(context.Background(), "", "msg"), ErrEmptyChannel)
	ch, err := s.Subscribe(context.Background(), "")
	require.ErrorIs(t, err, ErrEmptyChannel)
	assert.Nil(t, ch)
}

func TestRedisPubSubStore_NilContext(t *testing.T) {
	t.Parallel()
	client, _ := newRedisClientMock(t)
	s := &RedisPubSubStore{Client: client}
	require.ErrorIs(t, s.Publish(nilContext(), "ch", "msg"), ErrNilContext)
	ch, err := s.Subscribe(nilContext(), "ch")
	require.ErrorIs(t, err, ErrNilContext)
	assert.Nil(t, ch)
}

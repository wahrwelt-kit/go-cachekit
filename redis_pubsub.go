package cachekit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// OnDropFunc is called when a message is dropped because SendTimeout was exceeded
// Return quickly to avoid blocking the subscribe loop. Optional; nil means no callback
type OnDropFunc func(channel, payload string)

// RedisPubSubStore implements PubSubStore using a Redis client. Client must be non-nil
type RedisPubSubStore struct {
	// Client is the Redis client used for Publish and Subscribe. Must not be nil
	Client *redis.Client
	// ChanBufferSize is the buffer size for the channel returned by Subscribe
	// zero or negative uses 64
	ChanBufferSize int
	// SendTimeout limits how long the subscribe goroutine waits to send a message
	// when exceeded, the message is dropped and OnDrop is called if set. Zero uses 30s
	SendTimeout time.Duration
	// OnDrop is called when a message is dropped due to SendTimeout; nil disables the callback
	OnDrop OnDropFunc
}

var _ PubSubStore = (*RedisPubSubStore)(nil)

// Publish sends message to the given channel. Returns ErrRedisNotConfigured if the receiver or Client is nil
func (r *RedisPubSubStore) Publish(ctx context.Context, channel, message string) error {
	if r == nil || r.Client == nil {
		return ErrRedisNotConfigured
	}
	return r.Client.Publish(ctx, channel, message).Err()
}

// Subscribe returns a channel that receives messages published to the given channel
// A background goroutine reads from Redis until ctx is cancelled
// Caller must cancel ctx when done (e.g. defer cancel()) to release the goroutine
// and close the returned channel; otherwise the goroutine may block and leak
// Do not stop reading from the channel without cancelling ctx
// Returns ErrRedisNotConfigured if the receiver or Client is nil. See package doc for usage
func (r *RedisPubSubStore) Subscribe(ctx context.Context, channel string) (<-chan string, error) {
	if r == nil || r.Client == nil {
		return nil, ErrRedisNotConfigured
	}
	pubsub := r.Client.Subscribe(ctx, channel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, fmt.Errorf("pubsub subscribe: %w", err)
	}
	buf := r.ChanBufferSize
	if buf <= 0 {
		buf = 64
	}
	sendTimeout := r.SendTimeout
	if sendTimeout <= 0 {
		sendTimeout = 30 * time.Second
	}
	out := make(chan string, buf)
	onDrop := r.OnDrop
	go func() {
		defer close(out)
		defer func() { _ = pubsub.Close() }()
		var timer *time.Timer
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case msg, ok := <-pubsub.Channel():
				if !ok {
					if timer != nil {
						timer.Stop()
					}
					return
				}
				if timer == nil {
					timer = time.NewTimer(sendTimeout)
				} else {
					timer.Reset(sendTimeout)
				}
				select {
				case out <- msg.Payload:
					timer.Stop()
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					if onDrop != nil {
						onDrop(channel, msg.Payload)
					}
				}
			}
		}
	}()
	return out, nil
}

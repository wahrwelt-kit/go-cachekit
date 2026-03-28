package cachekit

import "context"

// PubSubStore provides publish and subscribe for a named channel
// Implementations must support concurrent use. Callers must cancel the context passed to Subscribe when done to avoid goroutine leaks
type PubSubStore interface {
	// Publish sends message to the given channel. Returns an error if the backend is unavailable
	Publish(ctx context.Context, channel, message string) error
	// Subscribe returns a channel that receives messages for the given channel. The subscription is active until ctx is cancelled; cancel ctx to release resources and close the returned channel
	Subscribe(ctx context.Context, channel string) (<-chan string, error)
}

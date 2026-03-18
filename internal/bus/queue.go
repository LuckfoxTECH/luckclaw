package bus

import (
	"context"
	"sync"
)

type MessageBus struct {
	Inbound  chan InboundMessage
	Outbound chan OutboundMessage

	closeOnce sync.Once
	closed    chan struct{}
}

// New creates a MessageBus with default capacity (100 for both queues).
func New() *MessageBus {
	return NewWithCapacity(100, 100)
}

// NewWithCapacity creates a MessageBus with the given channel capacities.
// Use gateway.inboundQueueCap and gateway.outboundQueueCap in config to tune.
func NewWithCapacity(inboundCap, outboundCap int) *MessageBus {
	if inboundCap < 1 {
		inboundCap = 100
	}
	if outboundCap < 1 {
		outboundCap = 100
	}
	return &MessageBus{
		Inbound:  make(chan InboundMessage, inboundCap),
		Outbound: make(chan OutboundMessage, outboundCap),
		closed:   make(chan struct{}),
	}
}

func (b *MessageBus) PublishInbound(ctx context.Context, msg InboundMessage) error {
	select {
	case <-b.closed:
		return context.Canceled
	default:
	}
	select {
	case b.Inbound <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.closed:
		return context.Canceled
	}
}

func (b *MessageBus) PublishOutbound(ctx context.Context, msg OutboundMessage) error {
	select {
	case <-b.closed:
		return context.Canceled
	default:
	}
	select {
	case b.Outbound <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.closed:
		return context.Canceled
	}
}

// Close signals the bus is shutting down. Pending publishes are unblocked
// and future publishes return context.Canceled.
func (b *MessageBus) Close() {
	b.closeOnce.Do(func() {
		close(b.closed)
	})
}

package openaiapi

import (
	"context"
)

// FailoverClient wraps multiple clients and tries the next on retryable failure.
type FailoverClient struct {
	Clients []*Client
}

func NewFailoverClient(clients []*Client) *FailoverClient {
	return &FailoverClient{Clients: clients}
}

func (f *FailoverClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	var lastErr error
	for _, c := range f.Clients {
		if c == nil {
			continue
		}
		res, err := c.Chat(ctx, req)
		if err == nil {
			return res, nil
		}
		lastErr = err
		fe, ok := err.(*FailoverError)
		if !ok || !fe.Retryable() {
			return ChatResult{}, err
		}
	}
	return ChatResult{}, lastErr
}

func (f *FailoverClient) ChatStream(ctx context.Context, req ChatRequest, cb StreamCallbacks) (ChatResult, error) {
	var lastErr error
	for _, c := range f.Clients {
		if c == nil {
			continue
		}
		res, err := c.ChatStream(ctx, req, cb)
		if err == nil {
			return res, nil
		}
		lastErr = err
		fe, ok := err.(*FailoverError)
		if !ok || !fe.Retryable() {
			return ChatResult{}, err
		}
	}
	return ChatResult{}, lastErr
}

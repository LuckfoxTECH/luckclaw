package agent

import (
	"context"
)

// GlobalSemaphore limits the total number of concurrent agent runs.
// When max <= 0, Acquire always succeeds (no limit).
type GlobalSemaphore struct {
	ch chan struct{}
}

func NewGlobalSemaphore(max int) *GlobalSemaphore {
	if max <= 0 {
		return nil
	}
	return &GlobalSemaphore{ch: make(chan struct{}, max)}
}

func (s *GlobalSemaphore) Acquire(ctx context.Context) bool {
	if s == nil {
		return true
	}
	select {
	case s.ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *GlobalSemaphore) Release() {
	if s == nil {
		return
	}
	select {
	case <-s.ch:
	default:
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"luckclaw/internal/providers/openaiapi"
)

// Attempt records a single LLM call attempt for structured error tracking.
type Attempt struct {
	Number    int    `json:"number"`
	Reason    string `json:"reason"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}

// RetryExhaustedError is returned when all retry attempts have been
// consumed. It carries the full attempt history so callers can inspect
// or log the failure trajectory.
type RetryExhaustedError struct {
	Attempts []Attempt
	Last     error
}

func (e *RetryExhaustedError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("all %d attempt(s) failed: ", len(e.Attempts)))
	for i, a := range e.Attempts {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(fmt.Sprintf("#%d [%s] %s", a.Number, a.Reason, a.Error))
	}
	return sb.String()
}

func (e *RetryExhaustedError) Unwrap() error { return e.Last }

// AttemptsJSON returns a compact JSON representation of the attempts
// slice, suitable for structured logging.
func AttemptsJSON(attempts []Attempt) string {
	b, err := json.Marshal(attempts)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// chatWithRetry wraps Provider.Chat with exponential-backoff retry.
// Only transient errors (rate_limit, timeout, server) are retried;
// non-retryable errors (auth, billing, format) abort immediately.
func (a *AgentLoop) chatWithRetry(ctx context.Context, req openaiapi.ChatRequest) (openaiapi.ChatResult, error) {
	maxRetries := a.Config.Agents.Defaults.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	baseDelay := time.Duration(a.Config.Agents.Defaults.RetryBaseDelay) * time.Millisecond
	if baseDelay <= 0 {
		baseDelay = 1 * time.Second
	}
	maxDelay := time.Duration(a.Config.Agents.Defaults.RetryMaxDelay) * time.Millisecond
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}

	var attempts []Attempt

	for attempt := 1; attempt <= maxRetries; attempt++ {
		res, err := a.Provider.Chat(ctx, req)
		if err == nil {
			if len(attempts) > 0 && a.Logger != nil {
				a.Logger.Info(fmt.Sprintf("LLM succeeded on attempt %d/%d (previous failures: %d)",
					attempt, maxRetries, len(attempts)))
			}
			return res, nil
		}

		if ctx.Err() != nil {
			return openaiapi.ChatResult{}, ctx.Err()
		}

		fe, ok := err.(*openaiapi.FailoverError)
		if !ok {
			return openaiapi.ChatResult{}, err
		}

		att := Attempt{
			Number:    attempt,
			Reason:    string(fe.Reason),
			Status:    fe.Status,
			Error:     fe.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		}
		attempts = append(attempts, att)

		if a.Logger != nil {
			a.Logger.Error(fmt.Sprintf("LLM attempt %d/%d failed [%s]: %v",
				attempt, maxRetries, fe.Reason, err))
		}

		if !fe.Retryable() {
			return openaiapi.ChatResult{}, &RetryExhaustedError{Attempts: attempts, Last: err}
		}

		if attempt < maxRetries {
			delay := backoffDelay(attempt, baseDelay, maxDelay)
			if a.Logger != nil {
				a.Logger.Info(fmt.Sprintf("Retrying in %v...", delay))
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return openaiapi.ChatResult{}, ctx.Err()
			}
		}
	}

	return openaiapi.ChatResult{}, &RetryExhaustedError{
		Attempts: attempts,
		Last:     fmt.Errorf("max retries exhausted"),
	}
}

// chatWithRetryStream wraps StreamingChatClient.ChatStream with retry.
// Falls back to Chat if provider does not implement StreamingChatClient.
func (a *AgentLoop) chatWithRetryStream(ctx context.Context, req openaiapi.ChatRequest, cb openaiapi.StreamCallbacks) (openaiapi.ChatResult, error) {
	streamClient, ok := a.Provider.(openaiapi.StreamingChatClient)
	if !ok {
		return a.chatWithRetry(ctx, req)
	}
	maxRetries := a.Config.Agents.Defaults.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	baseDelay := time.Duration(a.Config.Agents.Defaults.RetryBaseDelay) * time.Millisecond
	if baseDelay <= 0 {
		baseDelay = 1 * time.Second
	}
	maxDelay := time.Duration(a.Config.Agents.Defaults.RetryMaxDelay) * time.Millisecond
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	var attempts []Attempt
	for attempt := 1; attempt <= maxRetries; attempt++ {
		res, err := streamClient.ChatStream(ctx, req, cb)
		if err == nil {
			if len(attempts) > 0 && a.Logger != nil {
				a.Logger.Info(fmt.Sprintf("LLM stream succeeded on attempt %d/%d", attempt, maxRetries))
			}
			return res, nil
		}
		if ctx.Err() != nil {
			return openaiapi.ChatResult{}, ctx.Err()
		}
		fe, ok := err.(*openaiapi.FailoverError)
		if !ok {
			return openaiapi.ChatResult{}, err
		}
		att := Attempt{
			Number:    attempt,
			Reason:    string(fe.Reason),
			Status:    fe.Status,
			Error:     fe.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		}
		attempts = append(attempts, att)
		if a.Logger != nil {
			a.Logger.Error(fmt.Sprintf("LLM stream attempt %d/%d failed [%s]: %v", attempt, maxRetries, fe.Reason, err))
		}
		if !fe.Retryable() {
			return openaiapi.ChatResult{}, &RetryExhaustedError{Attempts: attempts, Last: err}
		}
		if attempt < maxRetries {
			delay := backoffDelay(attempt, baseDelay, maxDelay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return openaiapi.ChatResult{}, ctx.Err()
			}
		}
	}
	return openaiapi.ChatResult{}, &RetryExhaustedError{
		Attempts: attempts,
		Last:     fmt.Errorf("max retries exhausted"),
	}
}

func backoffDelay(attempt int, base, maxD time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(base) * exp)
	if delay > maxD {
		delay = maxD
	}
	jitter := time.Duration(rand.Int63n(int64(delay/2)+1)) - delay/4
	delay += jitter
	if delay < base/2 {
		delay = base / 2
	}
	return delay
}

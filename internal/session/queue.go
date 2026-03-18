package session

import (
	"context"
	"strings"
	"sync"
	"time"
)

type ChatFunc func(ctx context.Context, message, sessionKey, channel, chatID string, media []string) (string, bool, error)

type queueResult struct {
	Response string
	Streamed bool
	Err      error
}

type queueRequest struct {
	ctx        context.Context
	message    string
	sessionKey string
	channel    string
	chatID     string
	media      []string
	resultCh   chan queueResult
}

type inflightEntry struct {
	message string
	done    chan struct{}
	result  queueResult
}

type sessionWorker struct {
	mu       sync.Mutex
	reqCh    chan *queueRequest
	inflight *inflightEntry
}

// Queue provides per-session serial execution with deduplication.
// Each session key maps to a dedicated worker goroutine that processes
// requests one at a time. If the same message is already being processed,
// subsequent callers share the result instead of re-executing.
// When debounceMs > 0, multiple queued messages are merged after debounceMs
// of silence to reduce LLM calls and token usage.
type Queue struct {
	mu         sync.Mutex
	workers    map[string]*sessionWorker
	chatFn     ChatFunc
	idleTTL    time.Duration
	debounceMs time.Duration
	stopCh     chan struct{}
	wg         sync.WaitGroup
	cancelMap  map[string]context.CancelFunc
}

// CancelSession cancels the currently running request for the given session.
// Used when the user sends /stop to abort in-flight processing.
func (q *Queue) CancelSession(sessionKey string) {
	q.CancelSessionWithCount(sessionKey)
}

// CancelSessionWithCount cancels the currently running request for the given session.
// Returns 1 if a request was cancelled, 0 otherwise.
func (q *Queue) CancelSessionWithCount(sessionKey string) int {
	q.mu.Lock()
	cancel := q.cancelMap[sessionKey]
	delete(q.cancelMap, sessionKey)
	q.mu.Unlock()
	if cancel != nil {
		cancel()
		return 1
	}
	return 0
}

// CancelSessionsWithPrefix cancels all sessions whose key has the given prefix.
// Returns the number of sessions cancelled. Used for /stop to cancel subagent:parent:*.
func (q *Queue) CancelSessionsWithPrefix(prefix string) int {
	q.mu.Lock()
	var toCancel []context.CancelFunc
	for k, cancel := range q.cancelMap {
		if strings.HasPrefix(k, prefix) {
			toCancel = append(toCancel, cancel)
			delete(q.cancelMap, k)
		}
	}
	q.mu.Unlock()
	for _, cancel := range toCancel {
		cancel()
	}
	return len(toCancel)
}

// CancelSessionAndChildren cancels the session and its subagent/spawn children.
// For parent "channel:chatID", cancels: parent, subagent:parent:*, spawn:parent*.
// Returns total number of tasks cancelled. Used when user sends /stop.
func (q *Queue) CancelSessionAndChildren(parentSessionKey string) int {
	n := q.CancelSessionWithCount(parentSessionKey)
	n += q.CancelSessionsWithPrefix("subagent:" + parentSessionKey + ":")
	n += q.CancelSessionsWithPrefix("spawn:" + parentSessionKey)
	return n
}

func NewQueue(fn ChatFunc, debounceMs int) *Queue {
	dur := time.Duration(debounceMs) * time.Millisecond
	if debounceMs < 0 {
		dur = 0
	}
	return &Queue{
		workers:    make(map[string]*sessionWorker),
		chatFn:     fn,
		idleTTL:    5 * time.Minute,
		debounceMs: dur,
		stopCh:     make(chan struct{}),
		cancelMap:  make(map[string]context.CancelFunc),
	}
}

func (q *Queue) Submit(ctx context.Context, message, sessionKey, channel, chatID string, media []string) (string, bool, error) {
	select {
	case <-q.stopCh:
		return "", false, context.Canceled
	default:
	}

	w := q.getOrCreateWorker(sessionKey)

	// Dedup: if the exact same message is currently being processed for this
	// session, wait for its result instead of queuing a duplicate request.
	w.mu.Lock()
	if inf := w.inflight; inf != nil && inf.message == message {
		w.mu.Unlock()
		select {
		case <-inf.done:
			return inf.result.Response, inf.result.Streamed, inf.result.Err
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-q.stopCh:
			return "", false, context.Canceled
		}
	}
	w.mu.Unlock()

	req := &queueRequest{
		ctx:        ctx,
		message:    message,
		sessionKey: sessionKey,
		channel:    channel,
		chatID:     chatID,
		media:      media,
		resultCh:   make(chan queueResult, 1),
	}

	select {
	case w.reqCh <- req:
	case <-ctx.Done():
		return "", false, ctx.Err()
	case <-q.stopCh:
		return "", false, context.Canceled
	}

	select {
	case res := <-req.resultCh:
		return res.Response, res.Streamed, res.Err
	case <-ctx.Done():
		return "", false, ctx.Err()
	case <-q.stopCh:
		return "", false, context.Canceled
	}
}

// Shutdown signals all workers to stop and waits for in-flight requests
// to finish. New submissions are rejected after Shutdown is called.
func (q *Queue) Shutdown() {
	q.mu.Lock()
	select {
	case <-q.stopCh:
		q.mu.Unlock()
		return
	default:
		close(q.stopCh)
	}
	var toCancel []context.CancelFunc
	for k, cancel := range q.cancelMap {
		if cancel != nil {
			toCancel = append(toCancel, cancel)
		}
		delete(q.cancelMap, k)
	}
	q.mu.Unlock()
	for _, cancel := range toCancel {
		cancel()
	}
	q.wg.Wait()
}

func (q *Queue) getOrCreateWorker(key string) *sessionWorker {
	q.mu.Lock()
	defer q.mu.Unlock()

	if w, ok := q.workers[key]; ok {
		return w
	}

	w := &sessionWorker{
		reqCh: make(chan *queueRequest, 100),
	}
	q.workers[key] = w
	q.wg.Add(1)
	go q.runWorker(key, w)
	return w
}

func (q *Queue) runWorker(key string, w *sessionWorker) {
	defer q.wg.Done()
	idle := time.NewTimer(q.idleTTL)
	defer idle.Stop()

	var debounce *time.Timer
	var batch []*queueRequest
	if q.debounceMs > 0 {
		debounce = time.NewTimer(q.debounceMs)
		debounce.Stop()
		defer func() {
			if debounce != nil {
				debounce.Stop()
			}
		}()
	}

	processBatch := func(reqs []*queueRequest) {
		if len(reqs) == 0 {
			return
		}
		// Merge: use first req's metadata, concatenate messages
		first := reqs[0]
		mergedMsg := first.message
		allMedia := append([]string(nil), first.media...)
		for i := 1; i < len(reqs); i++ {
			mergedMsg += "\n\n" + reqs[i].message
			allMedia = append(allMedia, reqs[i].media...)
		}

		inf := &inflightEntry{
			message: mergedMsg,
			done:    make(chan struct{}),
		}
		w.mu.Lock()
		w.inflight = inf
		w.mu.Unlock()

		runCtx, runCancel := context.WithCancel(first.ctx)
		q.mu.Lock()
		q.cancelMap[key] = runCancel
		q.mu.Unlock()

		resp, streamed, err := q.chatFn(runCtx, mergedMsg, first.sessionKey, first.channel, first.chatID, allMedia)

		q.mu.Lock()
		delete(q.cancelMap, key)
		q.mu.Unlock()
		runCancel()

		inf.result = queueResult{Response: resp, Streamed: streamed, Err: err}
		close(inf.done)

		w.mu.Lock()
		w.inflight = nil
		w.mu.Unlock()

		result := queueResult{Response: resp, Streamed: streamed, Err: err}
		for _, r := range reqs {
			if r.ctx != nil && r.ctx.Err() != nil {
				continue
			}
			select {
			case r.resultCh <- result:
			default:
			}
		}
	}

	for {
		if q.debounceMs > 0 && len(batch) > 0 {
			// Collect mode: wait for debounce timer or more reqs
			select {
			case <-q.stopCh:
				q.drainWorker(w)
				for _, r := range batch {
					select {
					case r.resultCh <- queueResult{Err: context.Canceled}:
					default:
					}
				}
				q.mu.Lock()
				delete(q.workers, key)
				q.mu.Unlock()
				return

			case req := <-w.reqCh:
				batch = append(batch, req)
				for {
					select {
					case r := <-w.reqCh:
						batch = append(batch, r)
					default:
						goto doneDrain
					}
				}
			doneDrain:
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(q.debounceMs)

			case <-debounce.C:
				reqs := batch
				batch = nil
				processBatch(reqs)
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(q.idleTTL)

			case <-idle.C:
				q.mu.Lock()
				if len(w.reqCh) == 0 && len(batch) == 0 {
					delete(q.workers, key)
					q.mu.Unlock()
					return
				}
				q.mu.Unlock()
				idle.Reset(q.idleTTL)
			}
		} else {
			// No debounce or empty batch: wait for first req
			select {
			case <-q.stopCh:
				q.drainWorker(w)
				q.mu.Lock()
				delete(q.workers, key)
				q.mu.Unlock()
				return

			case req := <-w.reqCh:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(q.idleTTL)

				if q.debounceMs > 0 {
					batch = []*queueRequest{req}
					debounce.Reset(q.debounceMs)
				} else {
					processBatch([]*queueRequest{req})
				}

			case <-idle.C:
				q.mu.Lock()
				if len(w.reqCh) == 0 {
					delete(q.workers, key)
					q.mu.Unlock()
					return
				}
				q.mu.Unlock()
				idle.Reset(q.idleTTL)
			}
		}
	}
}

func (q *Queue) drainWorker(w *sessionWorker) {
	for {
		select {
		case req := <-w.reqCh:
			select {
			case req.resultCh <- queueResult{Err: context.Canceled}:
			default:
			}
		default:
			return
		}
	}
}

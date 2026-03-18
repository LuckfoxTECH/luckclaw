package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type Schedule struct {
	Kind    string `json:"kind"`
	AtMs    int64  `json:"atMs,omitempty"`
	EveryMs int64  `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type Payload struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Deliver      bool   `json:"deliver,omitempty"`
	ReminderOnly bool   `json:"reminderOnly,omitempty"` // // Only send the reminder content, without invoking the agent for processing
	Channel      string `json:"channel,omitempty"`
	To           string `json:"to,omitempty"`
}

type State struct {
	NextRunAtMs int64  `json:"nextRunAtMs,omitempty"`
	LastRunAtMs int64  `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type Job struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Schedule       Schedule `json:"schedule"`
	Payload        Payload  `json:"payload"`
	State          State    `json:"state"`
	CreatedAtMs    int64    `json:"createdAtMs"`
	UpdatedAtMs    int64    `json:"updatedAtMs"`
	DeleteAfterRun bool     `json:"deleteAfterRun,omitempty"`
}

type Store struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}
type Service struct {
	storePath   string
	mu          sync.Mutex
	store       Store
	onJob       func(ctx context.Context, job Job) (string, error)
	parser      cron.Parser
	lastModTime int64
}

func NewService(storePath string) *Service {
	return &Service{
		storePath: storePath,
		store:     Store{Version: 1, Jobs: []Job{}},
		parser:    cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

func (s *Service) SetCallback(cb func(ctx context.Context, job Job) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onJob = cb
}

func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.store = Store{Version: 1, Jobs: []Job{}}
			return nil
		}
		return err
	}
	if fi, err := os.Stat(s.storePath); err == nil {
		s.lastModTime = fi.ModTime().UnixNano()
	}
	var st Store
	if err := json.Unmarshal(b, &st); err != nil {
		s.store = Store{Version: 1, Jobs: []Job{}}
		return nil
	}
	if st.Version == 0 {
		st.Version = 1
	}
	s.store = st
	return nil
}

func (s *Service) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.store, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.storePath, b, 0o600)
}

func (s *Service) List(includeDisabled bool) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.store.Jobs))
	for _, j := range s.store.Jobs {
		if includeDisabled || j.Enabled {
			out = append(out, j)
		}
	}
	return out, nil
}

func (s *Service) AddEvery(name string, message string, everySeconds int, deliver bool, reminderOnly bool, channel string, to string) (Job, error) {
	if everySeconds <= 0 {
		return Job{}, fmt.Errorf("everySeconds must be > 0")
	}
	now := nowMs()
	job := Job{
		ID:      newID(),
		Name:    name,
		Enabled: true,
		Schedule: Schedule{
			Kind:    "every",
			EveryMs: int64(everySeconds) * 1000,
		},
		Payload: Payload{
			Kind:         "agent_message",
			Message:      message,
			Deliver:      deliver,
			ReminderOnly: reminderOnly,
			Channel:      channel,
			To:           to,
		},
		State: State{
			NextRunAtMs: now + int64(everySeconds)*1000,
		},
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Jobs = append(s.store.Jobs, job)
	if err := s.Save(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Service) AddAt(name string, message string, at time.Time, deliver bool, reminderOnly bool, channel string, to string) (Job, error) {
	now := nowMs()
	job := Job{
		ID:      newID(),
		Name:    name,
		Enabled: true,
		Schedule: Schedule{
			Kind: "at",
			AtMs: at.UnixMilli(),
		},
		Payload: Payload{
			Kind:         "agent_message",
			Message:      message,
			Deliver:      deliver,
			ReminderOnly: reminderOnly,
			Channel:      channel,
			To:           to,
		},
		State: State{
			NextRunAtMs: at.UnixMilli(),
		},
		CreatedAtMs:    now,
		UpdatedAtMs:    now,
		DeleteAfterRun: true,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Jobs = append(s.store.Jobs, job)
	if err := s.Save(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Service) AddCron(name string, message string, expr string, deliver bool, reminderOnly bool, channel string, to string) (Job, error) {
	return s.AddCronTZ(name, message, expr, "", deliver, reminderOnly, channel, to)
}

func (s *Service) AddCronTZ(name string, message string, expr string, tz string, deliver bool, reminderOnly bool, channel string, to string) (Job, error) {
	now := time.Now()
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return Job{}, fmt.Errorf("invalid timezone %q: %w", tz, err)
		}
		now = now.In(loc)
	}

	sched, err := s.parser.Parse(expr)
	if err != nil {
		return Job{}, err
	}
	next := sched.Next(now).UnixMilli()
	job := Job{
		ID:      newID(),
		Name:    name,
		Enabled: true,
		Schedule: Schedule{
			Kind: "cron",
			Expr: expr,
			TZ:   tz,
		},
		Payload: Payload{
			Kind:         "agent_message",
			Message:      message,
			Deliver:      deliver,
			ReminderOnly: reminderOnly,
			Channel:      channel,
			To:           to,
		},
		State: State{
			NextRunAtMs: next,
		},
		CreatedAtMs: nowMs(),
		UpdatedAtMs: nowMs(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Jobs = append(s.store.Jobs, job)
	if err := s.Save(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Service) Enable(jobID string, enabled bool) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID == jobID {
			s.store.Jobs[i].Enabled = enabled
			s.store.Jobs[i].UpdatedAtMs = nowMs()
			if err := s.Save(); err != nil {
				return nil, err
			}
			j := s.store.Jobs[i]
			return &j, nil
		}
	}
	return nil, nil
}

func (s *Service) Remove(jobID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.store.Jobs[:0]
	removed := false
	for _, j := range s.store.Jobs {
		if j.ID == jobID {
			removed = true
			continue
		}
		out = append(out, j)
	}
	s.store.Jobs = out
	if !removed {
		return false, nil
	}
	return true, s.Save()
}

func (s *Service) RunJob(ctx context.Context, jobID string, force bool) (bool, error) {
	s.mu.Lock()
	var job *Job
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID == jobID {
			job = &s.store.Jobs[i]
			break
		}
	}
	s.mu.Unlock()
	if job == nil {
		return false, nil
	}
	if !force && !job.Enabled {
		return false, nil
	}
	return true, s.executeAndPersist(ctx, jobID)
}

func (s *Service) Run(ctx context.Context) error {
	log.Printf("[cron] Run: started, storePath=%s", s.storePath)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.runDue(ctx)
		}
	}
}

func (s *Service) runDue(ctx context.Context) {
	if fi, err := os.Stat(s.storePath); err == nil {
		mt := fi.ModTime().UnixNano()
		s.mu.Lock()
		changed := mt != s.lastModTime
		if changed {
			s.lastModTime = mt
		}
		s.mu.Unlock()
		if changed {
			_ = s.Reload()
		}
	}

	now := nowMs()
	var due []string

	s.mu.Lock()
	for _, j := range s.store.Jobs {
		if !j.Enabled {
			continue
		}
		if j.State.NextRunAtMs > 0 && j.State.NextRunAtMs <= now {
			due = append(due, j.ID)
		}
	}
	s.mu.Unlock()

	if len(due) > 0 {
		log.Printf("[cron] runDue: found %d due job(s): %v", len(due), due)
	}
	for _, id := range due {
		_ = s.executeAndPersist(ctx, id)
	}
}

func (s *Service) executeAndPersist(ctx context.Context, jobID string) error {
	s.mu.Lock()
	var idx = -1
	for i := range s.store.Jobs {
		if s.store.Jobs[i].ID == jobID {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.mu.Unlock()
		return nil
	}
	job := s.store.Jobs[idx]
	cb := s.onJob
	s.mu.Unlock()

	log.Printf("[cron] executeAndPersist: job=%s name=%q deliver=%v channel=%q to=%q msg=%q",
		jobID, job.Name, job.Payload.Deliver, job.Payload.Channel, job.Payload.To, job.Payload.Message)

	var result string
	var err error
	if cb != nil {
		result, err = cb(ctx, job)
		if err != nil {
			log.Printf("[cron] executeAndPersist: job=%s callback error: %v", jobID, err)
		}
	} else {
		log.Printf("[cron] executeAndPersist: job=%s WARNING: no callback registered", jobID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= len(s.store.Jobs) || s.store.Jobs[idx].ID != jobID {
		return nil
	}
	now := nowMs()
	s.store.Jobs[idx].State.LastRunAtMs = now
	if err != nil {
		s.store.Jobs[idx].State.LastStatus = "error"
		s.store.Jobs[idx].State.LastError = err.Error()
	} else {
		_ = result
		s.store.Jobs[idx].State.LastStatus = "ok"
		s.store.Jobs[idx].State.LastError = ""
	}
	s.store.Jobs[idx].UpdatedAtMs = now
	s.store.Jobs[idx].State.NextRunAtMs = s.nextRunAtMs(s.store.Jobs[idx])
	if s.store.Jobs[idx].DeleteAfterRun {
		out := s.store.Jobs[:0]
		for _, j := range s.store.Jobs {
			if j.ID == jobID {
				continue
			}
			out = append(out, j)
		}
		s.store.Jobs = out
	}
	return s.Save()
}

func (s *Service) nextRunAtMs(job Job) int64 {
	now := time.Now()
	switch job.Schedule.Kind {
	case "every":
		if job.Schedule.EveryMs <= 0 {
			return 0
		}
		return now.UnixMilli() + job.Schedule.EveryMs
	case "at":
		return 0
	case "cron":
		sched, err := s.parser.Parse(job.Schedule.Expr)
		if err != nil {
			return 0
		}
		ref := now
		if job.Schedule.TZ != "" {
			if loc, err := time.LoadLocation(job.Schedule.TZ); err == nil {
				ref = now.In(loc)
			}
		}
		return sched.Next(ref).UnixMilli()
	default:
		return 0
	}
}

func (s *Service) Reload() error {
	return s.Load()
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

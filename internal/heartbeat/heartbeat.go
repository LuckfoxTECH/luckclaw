package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Service struct {
	Workspace   string
	Interval    time.Duration
	OnHeartbeat func(ctx context.Context, content string) (string, error)
	OnResult    func(ctx context.Context, result string)

	// OnDecide is an optional two-phase callback. If set, it is called first
	// with the HEARTBEAT.md content. It should return "run" to proceed with
	// the full agent loop, or any other value (e.g. "skip") to skip this tick.
	// This saves tokens by letting a lightweight LLM call decide if work is needed.
	OnDecide func(ctx context.Context, content string) (string, error)
}

func New(workspace string, intervalSeconds int) *Service {
	interval := time.Duration(intervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &Service{
		Workspace: workspace,
		Interval:  interval,
	}
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) error {
	if s.OnHeartbeat == nil {
		return nil
	}
	path := filepath.Join(s.Workspace, "HEARTBEAT.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return nil
	}

	// Phase 1: lightweight decision — should we run this heartbeat?
	if s.OnDecide != nil {
		decision, err := s.OnDecide(ctx, content)
		if err != nil {
			return nil
		}
		if strings.ToLower(strings.TrimSpace(decision)) != "run" {
			return nil
		}
	}

	// Phase 2: full agent execution
	result, _ := s.OnHeartbeat(ctx, content)
	if result != "" && s.OnResult != nil {
		s.OnResult(ctx, result)
	}
	return nil
}

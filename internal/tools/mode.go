package tools

import "context"

type RunMode string

const (
	RunModeBuild RunMode = "Build"
	RunModePlan  RunMode = "Plan"
)

const ctxKeyRunMode contextKey = "run_mode"

func WithRunMode(ctx context.Context, mode RunMode) context.Context {
	if mode == "" {
		mode = RunModeBuild
	}
	return context.WithValue(ctx, ctxKeyRunMode, mode)
}

func RunModeFromContext(ctx context.Context) RunMode {
	if v := ctx.Value(ctxKeyRunMode); v != nil {
		if m, ok := v.(RunMode); ok {
			if m != "" {
				return m
			}
		}
		if s, ok := v.(string); ok && s != "" {
			return RunMode(s)
		}
	}
	return RunModeBuild
}

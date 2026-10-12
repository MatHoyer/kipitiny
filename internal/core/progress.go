package core

import (
	"context"

	"github.com/MatHoyer/kipitiny/internal/progress"
)

// WithProgress has a long call under ctx (connecting or testing an
// integration) call f with each step as it starts, e.g. "login".
func WithProgress(ctx context.Context, f func(step string)) context.Context {
	return progress.With(ctx, f)
}

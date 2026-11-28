package mcp

import (
	"fmt"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
)

// An invalid input that wraps the cause keeps its own explanation, even when
// the cause is a sentinel friendly would otherwise rewrite.
func TestFriendlyKeepsInvalidReason(t *testing.T) {
	for _, cause := range []error{app.ErrBusy, domain.ErrNotFound} {
		err := fmt.Errorf("%w: networks saved, but not applied yet: %w", app.ErrInvalid, cause)
		if got := friendly(err); got.Error() != err.Error() {
			t.Errorf("wrapping %v: %q, want %q", cause, got, err)
		}
	}
}

package mcp

import (
	"fmt"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/core"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// An invalid input that wraps the cause keeps its own explanation, even when
// the cause is a sentinel friendly would otherwise rewrite.
func TestFriendlyKeepsInvalidReason(t *testing.T) {
	for _, cause := range []error{core.ErrBusy, domain.ErrNotFound} {
		err := fmt.Errorf("%w: networks saved, but not applied yet: %w", core.ErrInvalid, cause)
		if got := friendly(err); got.Error() != err.Error() {
			t.Errorf("wrapping %v: %q, want %q", cause, got, err)
		}
	}
}

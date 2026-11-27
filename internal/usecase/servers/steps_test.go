package servers

import (
	"context"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
)

func TestCleanupSteps(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	c.cleaning.Store(true)
	c.steps.SetOn(cleanupKey, CleanupImages, "edge-1")
	if v := c.Cleanup(ctx); v.Step != CleanupImages || v.StepServer != "edge-1" {
		t.Errorf("running: step = %q on %q", v.Step, v.StepServer)
	}
	c.cleaning.Store(false)
	if v := c.Cleanup(ctx); v.Step != "" || v.StepServer != "" {
		t.Errorf("idle: step = %q on %q", v.Step, v.StepServer)
	}
}

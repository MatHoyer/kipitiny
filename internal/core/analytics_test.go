package core

import (
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
)

func TestOwnImageRef(t *testing.T) {
	c := newTestCore(t, config.Config{Version: "0.36.0", Update: config.Update{Image: "registry.local/kipitiny"}})
	if got := c.ownImageRef(); got != "registry.local/kipitiny:0.36.0" {
		t.Errorf("release: %s", got)
	}
	c.cfg.Version = "0.36.0-3-gabc-dirty"
	if got := c.ownImageRef(); got != "registry.local/kipitiny:dev" {
		t.Errorf("dev build: %s", got)
	}
	c.cfg.Version = "dev-feat-x"
	if got := c.ownImageRef(); got != "registry.local/kipitiny:dev-feat-x" {
		t.Errorf("test environment: %s", got)
	}
}

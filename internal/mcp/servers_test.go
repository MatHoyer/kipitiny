package mcp

import (
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// TestRebootServerConfirm covers the refusals; rebooting needs a host.
func TestRebootServerConfirm(t *testing.T) {
	e := newEnv(t)
	read, admin := e.session(core.PermView), e.session()
	if out, isErr := e.call(read, "reboot_server", map[string]any{"confirm": "local"}); !isErr || !strings.Contains(out, "forbidden") {
		t.Errorf("read token: %s", out)
	}
	if out, isErr := e.call(admin, "reboot_server", map[string]any{"confirm": "nope"}); !isErr || !strings.Contains(out, "confirm must repeat the server name") {
		t.Errorf("unconfirmed: %s", out)
	}
}

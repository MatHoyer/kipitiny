package core

import (
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/upgrade"
)

func TestUpdateEvent(t *testing.T) {
	if e := updateEvent("0.27.0", "0.28.0", nil); e.Level != notify.Info || strings.Contains(e.Message, "Before upgrading") {
		t.Errorf("no notices: %+v", e)
	}
	e := updateEvent("0.27.0", "0.29.0", []upgrade.Notice{
		{Version: "0.29.0", Level: upgrade.Info, Text: "Backups move."},
		{Version: "0.28.0", Level: upgrade.Warning, Text: "Traefik restarts."},
	})
	if e.Level != notify.Warning || !strings.Contains(e.Message, "Before upgrading:\n- Backups move. (0.29.0)\n- Traefik restarts. (0.28.0)") {
		t.Errorf("notices: %+v", e)
	}
}

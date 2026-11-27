package deploy

import (
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
)

func TestApplyJobInput(t *testing.T) {
	ok := JobInput{Name: "nightly", Command: "echo hi", Cron: "0 3 * * *", Enabled: true}
	var j domain.Job
	if err := ApplyJobInput(&j, ok, cron.Cron{}); err != nil {
		t.Fatal(err)
	}
	if j.TimeoutSec != DefaultJobTimeout {
		t.Errorf("default timeout: %d", j.TimeoutSec)
	}
	for name, in := range map[string]JobInput{
		"name":         {Name: "Nightly", Command: "x", Cron: "@daily"},
		"no command":   {Name: "x", Command: "  ", Cron: "@daily"},
		"cron":         {Name: "x", Command: "x", Cron: "every day"},
		"long timeout": {Name: "x", Command: "x", Cron: "@daily", TimeoutSec: MaxJobTimeout + 1},
		"neg timeout":  {Name: "x", Command: "x", Cron: "@daily", TimeoutSec: -1},
	} {
		if err := ApplyJobInput(&j, in, cron.Cron{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

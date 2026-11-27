package deploy

import (
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// A job's timeout, in seconds, when it sets none, and at most.
const (
	DefaultJobTimeout = 60 * 60
	MaxJobTimeout     = 24 * 60 * 60
)

// JobInput is a job as a user or a compose file declares it.
type JobInput struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Cron       string `json:"cron"`
	TimeoutSec int    `json:"timeoutSec"` // 0: one hour
	Enabled    bool   `json:"enabled"`
}

// ApplyJobInput validates in (its schedule with cron) and sets it on j.
func ApplyJobInput(j *domain.Job, in JobInput, cron domain.Cron) error {
	in.Name, in.Command, in.Cron = strings.TrimSpace(in.Name), strings.TrimSpace(in.Command), strings.TrimSpace(in.Cron)
	if !domain.ValidName(in.Name) {
		return fmt.Errorf("%w: job name must be lowercase letters, digits and dashes", ErrInvalid)
	}
	if in.Command == "" {
		return fmt.Errorf("%w: a job needs a command", ErrInvalid)
	}
	if _, err := cron.Next(in.Cron, time.Now().UTC()); err != nil {
		return fmt.Errorf("%w: schedule: %s", ErrInvalid, err.Error())
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = DefaultJobTimeout
	}
	if in.TimeoutSec < 1 || in.TimeoutSec > MaxJobTimeout {
		return fmt.Errorf("%w: timeout must be between 1 second and 24 hours", ErrInvalid)
	}
	j.Name, j.Command, j.Cron, j.TimeoutSec, j.Enabled = in.Name, in.Command, in.Cron, in.TimeoutSec, in.Enabled
	return nil
}

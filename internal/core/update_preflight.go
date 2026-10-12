package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// UpdatePreflight says whether now is a good time to update the manager:
// the update restarts it, cutting short what runs longer than its drain.
type UpdatePreflight struct {
	Checks []PreflightCheck `json:"checks"`
	// Ready: no check blocks the update.
	Ready bool `json:"ready"`
}

type PreflightCheck struct {
	// ID is version, tasks, docker or backup.
	ID string `json:"id"`
	// State is ok, warn (the update may go ahead) or block.
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// managerBackupFresh is how recent a manager backup should be to update
// without a warning: the update keeps the data, but a backup is the way back.
const managerBackupFresh = 24 * time.Hour

func (c *Core) UpdatePreflight(ctx context.Context) UpdatePreflight {
	u := c.Update()
	var checks []PreflightCheck
	add := func(id, state, detail string) {
		checks = append(checks, PreflightCheck{ID: id, State: state, Detail: detail})
	}

	switch {
	case u.Updating:
		add("version", "block", "an update is already running")
	case !u.Available:
		add("version", "block", "already up to date")
	case !u.CanApply:
		add("version", "block", u.Reason)
	default:
		add("version", "ok", u.Current+" → "+u.Latest)
	}

	if tasks := c.runningTasks(ctx); len(tasks) > 0 {
		add("tasks", "block", "in progress: "+strings.Join(tasks, ", ")+". The restart would cut it short")
	} else {
		add("tasks", "ok", "no deploy, backup, restore or cleanup running")
	}

	if info, err := c.dockerFor(store.LocalServerID).Info(ctx); err != nil {
		add("docker", "block", "Docker doesn't answer: "+err.Error())
	} else {
		add("docker", "ok", "Docker "+info.Version+" answers")
	}

	bs, err := c.store.ListBackups(ctx, store.BackupFilter{Kind: store.BackupKindManager, Status: store.OpSucceeded, Limit: 1})
	switch {
	case err != nil:
		add("backup", "warn", "couldn't read the manager's backups: "+err.Error())
	case len(bs) == 0:
		add("backup", "warn", "the manager was never backed up: back it up first to have a way back")
	case time.Since(bs[0].CreatedAt) > managerBackupFresh:
		add("backup", "warn", "the last manager backup is "+ago(bs[0].CreatedAt)+" old: back it up first to have a way back")
	default:
		add("backup", "ok", "the manager was backed up "+ago(bs[0].CreatedAt)+" ago")
	}

	ready := !slices.ContainsFunc(checks, func(ch PreflightCheck) bool { return ch.State == "block" })
	return UpdatePreflight{Checks: checks, Ready: ready}
}

// runningTasks names what the manager is doing that an update would cut
// short: the services with an operation (deploy, backup, restore…), restore
// tests, the cleanup, and any other background operation.
func (c *Core) runningTasks(ctx context.Context) []string {
	var out []string
	c.busy.Range(func(k, _ any) bool {
		if svc, err := c.store.GetService(ctx, k.(string)); err == nil {
			name := svc.Name
			if p, err := c.store.GetProject(ctx, svc.ProjectID); err == nil {
				name = p.Name + "/" + name
			}
			out = append(out, name)
		}
		return true
	})
	slices.Sort(out)
	if n := len(c.steps.running("verify/")); n > 0 {
		out = append(out, plural(n, "a restore test", "restore tests"))
	}
	if c.cleaning.Load() {
		out = append(out, "the cleanup")
	}
	if len(out) == 0 {
		if n := int(c.running.Load()); n > 0 {
			out = append(out, plural(n, "a background task", "background tasks"))
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ago is a rough age: "3 hours", "2 days".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return plural(max(int(d.Minutes()), 1), "a minute", "minutes")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "an hour", "hours")
	}
	return plural(int(d.Hours()/24), "a day", "days")
}

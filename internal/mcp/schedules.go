package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Backup schedules and uptime checks: not in compose files, so agents set
// them here.

type listSchedulesOut struct {
	Schedules []core.ScheduleView `json:"schedules"`
}

func (t *tools) listBackupSchedules(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, listSchedulesOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listSchedulesOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, listSchedulesOut{}, friendly(err)
	}
	scs, err := t.c.ListBackupSchedules(ctx, svc.ID)
	return nil, listSchedulesOut{Schedules: scs}, err
}

// setScheduleIn uses pointers so an update keeps what isn't given.
type setScheduleIn struct {
	Service     string  `json:"service" jsonschema:"the service as project/service, or its ID"`
	ScheduleID  string  `json:"schedule_id,omitempty" jsonschema:"a schedule to change (see list_backup_schedules); omit to add one"`
	Cron        *string `json:"cron,omitempty" jsonschema:"5-field cron in UTC or a descriptor like @daily; required for a new schedule"`
	Storage     *string `json:"storage,omitempty" jsonschema:"storage ID (see list_storage); default local disk"`
	Database    *string `json:"database,omitempty" jsonschema:"for PostgreSQL, which of the instance's databases to dump; default the service's own"`
	KeepLast    *int    `json:"keep_last,omitempty" jsonschema:"keep the N most recent backups (new schedule default 0)"`
	KeepDaily   *int    `json:"keep_daily,omitempty" jsonschema:"keep the newest backup of each of the last N days (default 7)"`
	KeepWeekly  *int    `json:"keep_weekly,omitempty" jsonschema:"same per ISO week (default 4)"`
	KeepMonthly *int    `json:"keep_monthly,omitempty" jsonschema:"same per month (default 6)"`
	Enabled     *bool   `json:"enabled,omitempty" jsonschema:"default true"`
	Verify      *bool   `json:"verify,omitempty" jsonschema:"restore-test each backup in a throwaway container (default false)"`
}

func (t *tools) setBackupSchedule(ctx context.Context, _ *mcp.CallToolRequest, in setScheduleIn) (*mcp.CallToolResult, core.ScheduleView, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "set_backup_schedule", in.Service, func() (core.ScheduleView, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return core.ScheduleView{}, err
		}
		sc := core.ScheduleInput{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, Enabled: true}
		if in.ScheduleID != "" {
			cur, err := t.schedule(ctx, svc.ID, in.ScheduleID)
			if err != nil {
				return core.ScheduleView{}, err
			}
			sc = core.ScheduleInput{TargetID: cur.TargetID, Database: cur.Database, Cron: cur.Cron, KeepLast: cur.KeepLast,
				KeepDaily: cur.KeepDaily, KeepWeekly: cur.KeepWeekly, KeepMonthly: cur.KeepMonthly, Enabled: cur.Enabled, Verify: cur.Verify}
		} else if in.Cron == nil {
			return core.ScheduleView{}, fmt.Errorf("a new schedule needs a cron")
		}
		set(&sc.Cron, in.Cron)
		set(&sc.TargetID, in.Storage)
		set(&sc.Database, in.Database)
		set(&sc.KeepLast, in.KeepLast)
		set(&sc.KeepDaily, in.KeepDaily)
		set(&sc.KeepWeekly, in.KeepWeekly)
		set(&sc.KeepMonthly, in.KeepMonthly)
		set(&sc.Enabled, in.Enabled)
		set(&sc.Verify, in.Verify)
		if in.ScheduleID != "" {
			return t.c.UpdateBackupSchedule(ctx, in.ScheduleID, sc)
		}
		return t.c.CreateBackupSchedule(ctx, svc.ID, sc)
	})
	return nil, out, err
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// schedule finds one of the service's schedules, so an ID can't reach
// another service's (or the manager's) schedule.
func (t *tools) schedule(ctx context.Context, serviceID, id string) (core.ScheduleView, error) {
	scs, err := t.c.ListBackupSchedules(ctx, serviceID)
	if err != nil {
		return core.ScheduleView{}, err
	}
	i := slices.IndexFunc(scs, func(sc core.ScheduleView) bool { return sc.ID == id })
	if i < 0 {
		return core.ScheduleView{}, fmt.Errorf("%w: schedule %s of this service", store.ErrNotFound, id)
	}
	return scs[i], nil
}

type deleteScheduleIn struct {
	Service    string `json:"service" jsonschema:"the service as project/service, or its ID"`
	ScheduleID string `json:"schedule_id" jsonschema:"the schedule (see list_backup_schedules); its backups are kept"`
}

func (t *tools) deleteBackupSchedule(ctx context.Context, _ *mcp.CallToolRequest, in deleteScheduleIn) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "delete_backup_schedule", in.Service, func() (deleted, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return deleted{}, err
		}
		if _, err := t.schedule(ctx, svc.ID, in.ScheduleID); err != nil {
			return deleted{}, err
		}
		return deleted{Deleted: in.ScheduleID}, t.c.DeleteBackupSchedule(ctx, in.ScheduleID)
	})
	return nil, out, err
}

type setUptimeIn struct {
	Service        string `json:"service" jsonschema:"an app with a public domain, as project/service or its ID"`
	Path           string `json:"path,omitempty" jsonschema:"path requested on the app's domain, default /"`
	IntervalSec    int    `json:"interval_sec,omitempty" jsonschema:"seconds between checks, 30 to 3600, default 60"`
	TimeoutSec     int    `json:"timeout_sec,omitempty" jsonschema:"seconds before a check fails, 1 to 60, default 10"`
	ExpectedStatus int    `json:"expected_status,omitempty" jsonschema:"HTTP status that counts as up; default any below 400"`
	Disabled       bool   `json:"disabled,omitempty" jsonschema:"keep the check and its history but pause it"`
}

func (t *tools) setUptimeCheck(ctx context.Context, _ *mcp.CallToolRequest, in setUptimeIn) (*mcp.CallToolResult, core.UptimeView, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "set_uptime_check", in.Service, func() (core.UptimeView, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return core.UptimeView{}, err
		}
		return t.c.SetUptime(ctx, svc.ID, core.UptimeInput{Path: in.Path, IntervalSec: in.IntervalSec, TimeoutSec: in.TimeoutSec,
			ExpectedStatus: in.ExpectedStatus, Enabled: !in.Disabled})
	})
	return nil, out, err
}

func (t *tools) deleteUptimeCheck(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "delete_uptime_check", in.Service, func() (deleted, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return deleted{}, err
		}
		return deleted{Deleted: in.Service}, t.c.DeleteUptime(ctx, svc.ID)
	})
	return nil, out, err
}

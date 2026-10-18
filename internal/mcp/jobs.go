package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type listJobsOut struct {
	Jobs []core.JobView `json:"jobs"`
}

func (t *tools) listJobs(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, listJobsOut, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, listJobsOut{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, listJobsOut{}, friendly(err)
	}
	js, err := t.c.ListJobs(ctx, svc.ID)
	return nil, listJobsOut{Jobs: js}, err
}

// setJobIn uses pointers so an update keeps what isn't given.
type setJobIn struct {
	Service    string  `json:"service" jsonschema:"an app, as project/service or its ID"`
	Job        string  `json:"job" jsonschema:"the job's name (lowercase letters, digits, dashes); a new name adds a job"`
	Rename     *string `json:"rename,omitempty" jsonschema:"a new name for an existing job"`
	Command    *string `json:"command,omitempty" jsonschema:"shell command run with sh -c in the app's image; required for a new job"`
	Cron       *string `json:"cron,omitempty" jsonschema:"5-field cron in UTC (CRON_TZ=<zone> prefix for another zone) or a descriptor like @hourly; required for a new job"`
	TimeoutSec *int    `json:"timeout_sec,omitempty" jsonschema:"seconds before the run is killed, 1 to 86400, default 3600"`
	Enabled    *bool   `json:"enabled,omitempty" jsonschema:"default true; false keeps the job and its history but stops scheduling it"`
}

func (t *tools) setJob(ctx context.Context, _ *mcp.CallToolRequest, in setJobIn) (*mcp.CallToolResult, core.JobView, error) {
	out, err := mutate(ctx, t.c, "set_job", in.Service, func() (core.JobView, error) {
		svc, err := t.service(ctx, in.Service, core.PermSettings)
		if err != nil {
			return core.JobView{}, err
		}
		cur, err := t.job(ctx, svc.ID, in.Job)
		exists := err == nil
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return core.JobView{}, err
		}
		j := core.JobInput{Name: in.Job, Enabled: true}
		if exists {
			j = core.JobInput{Name: cur.Name, Command: cur.Command, Cron: cur.Cron, TimeoutSec: cur.TimeoutSec, Enabled: cur.Enabled}
		} else if in.Command == nil || in.Cron == nil {
			return core.JobView{}, fmt.Errorf("a new job needs a command and a cron")
		}
		set(&j.Name, in.Rename)
		set(&j.Command, in.Command)
		set(&j.Cron, in.Cron)
		set(&j.TimeoutSec, in.TimeoutSec)
		set(&j.Enabled, in.Enabled)
		if exists {
			return t.c.UpdateJob(ctx, cur.ID, j)
		}
		return t.c.CreateJob(ctx, svc.ID, j)
	})
	return nil, out, err
}

type jobArg struct {
	Service string `json:"service" jsonschema:"the app, as project/service or its ID"`
	Job     string `json:"job" jsonschema:"the job's name (see list_jobs)"`
}

func (t *tools) deleteJob(ctx context.Context, _ *mcp.CallToolRequest, in jobArg) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, "delete_job", in.Service, func() (deleted, error) {
		svc, err := t.service(ctx, in.Service, core.PermSettings)
		if err != nil {
			return deleted{}, err
		}
		j, err := t.job(ctx, svc.ID, in.Job)
		if err != nil {
			return deleted{}, err
		}
		return deleted{Deleted: in.Job}, t.c.DeleteJob(ctx, j.ID)
	})
	return nil, out, err
}

func (t *tools) runJob(ctx context.Context, _ *mcp.CallToolRequest, in jobArg) (*mcp.CallToolResult, store.JobRun, error) {
	out, err := mutate(ctx, t.c, "run_job", in.Service, func() (store.JobRun, error) {
		svc, err := t.service(ctx, in.Service, core.PermDeploy)
		if err != nil {
			return store.JobRun{}, err
		}
		j, err := t.job(ctx, svc.ID, in.Job)
		if err != nil {
			return store.JobRun{}, err
		}
		return t.c.RunJob(ctx, j.ID)
	})
	return nil, out, err
}

type listJobRunsIn struct {
	Service string `json:"service" jsonschema:"the app, as project/service or its ID"`
	Job     string `json:"job" jsonschema:"the job's name (see list_jobs)"`
	RunID   string `json:"run_id,omitempty" jsonschema:"a run whose log to return; the latest when omitted"`
	Lines   int    `json:"lines,omitempty" jsonschema:"last log lines to return, default 200, max 2000"`
}

type listJobRunsOut struct {
	Runs []store.JobRun `json:"runs"`
	// Log is the given run's, or the latest one's.
	LogRunID string   `json:"log_run_id,omitempty"`
	Log      []string `json:"log"`
}

func (t *tools) listJobRuns(ctx context.Context, _ *mcp.CallToolRequest, in listJobRunsIn) (*mcp.CallToolResult, listJobRunsOut, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, listJobRunsOut{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, listJobRunsOut{}, friendly(err)
	}
	j, err := t.job(ctx, svc.ID, in.Job)
	if err != nil {
		return nil, listJobRunsOut{}, friendly(err)
	}
	runs, err := t.c.ListJobRuns(ctx, j.ID)
	if err != nil {
		return nil, listJobRunsOut{}, err
	}
	out := listJobRunsOut{Runs: runs, LogRunID: in.RunID, Log: []string{}}
	if out.LogRunID == "" && len(runs) > 0 {
		out.LogRunID = runs[0].ID
	}
	if out.LogRunID == "" {
		return nil, out, nil
	}
	r, err := t.c.JobRunLog(ctx, j.ID, out.LogRunID)
	if err != nil {
		return nil, listJobRunsOut{}, friendly(err)
	}
	defer r.Close()
	out.Log, err = tailLines(r, in.Lines)
	return nil, out, err
}

// job finds one of the service's jobs by name.
func (t *tools) job(ctx context.Context, serviceID, name string) (core.JobView, error) {
	js, err := t.c.ListJobs(ctx, serviceID)
	if err != nil {
		return core.JobView{}, err
	}
	i := slices.IndexFunc(js, func(j core.JobView) bool { return j.Name == name })
	if i < 0 {
		return core.JobView{}, fmt.Errorf("%w: job %s of this service", store.ErrNotFound, name)
	}
	return js[i], nil
}

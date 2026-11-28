package projects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

// RunJob runs a job now, unless it is running already.
func (c *Projects) RunJob(ctx context.Context, id string) (domain.JobRun, error) {
	return c.startJobRun(ctx, id)
}

func (c *Projects) ListJobRuns(ctx context.Context, jobID string) ([]domain.JobRun, error) {
	if _, err := c.store.GetJob(ctx, jobID); err != nil {
		return nil, err
	}
	return c.store.ListJobRuns(ctx, jobID, 0)
}

// JobRunLog opens a run's log; a missing log yields an empty reader.
func (c *Projects) JobRunLog(ctx context.Context, jobID, runID string) (io.ReadCloser, error) {
	j, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	r, err := c.store.GetJobRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if r.JobID != j.ID {
		return nil, domain.ErrNotFound
	}
	f, err := os.Open(c.jobLogPath(j.ServiceID, r.ID))
	if errors.Is(err, os.ErrNotExist) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f, err
}

func (c *Projects) startJobRun(ctx context.Context, id string) (domain.JobRun, error) {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return domain.JobRun{}, err
	}
	svc, err := c.store.GetService(ctx, j.ServiceID)
	if err != nil {
		return domain.JobRun{}, err
	}
	switch {
	case svc.CurrentDeploymentID == "":
		return domain.JobRun{}, fmt.Errorf("%w: deploy %s before running its jobs", ErrInvalid, svc.Name)
	case svc.Stopped:
		return domain.JobRun{}, fmt.Errorf("%w: %s is stopped", ErrInvalid, svc.Name)
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return domain.JobRun{}, err
	}

	runCtx, cancel := context.WithTimeout(c.bg, time.Duration(j.TimeoutSec)*time.Second)
	c.jobs.mu.Lock()
	if _, busy := c.jobs.running[j.ID]; busy {
		c.jobs.mu.Unlock()
		cancel()
		return domain.JobRun{}, fmt.Errorf("%w: job %s is running", ErrBusy, j.Name)
	}
	if c.jobs.running == nil {
		c.jobs.running = map[string]jobRunning{}
	}
	c.jobs.running[j.ID] = jobRunning{serviceID: svc.ID, projectID: project.ID, cancel: cancel}
	c.jobs.mu.Unlock()
	done := func() {
		cancel()
		c.jobs.mu.Lock()
		delete(c.jobs.running, j.ID)
		c.jobs.mu.Unlock()
	}

	r := domain.JobRun{JobID: j.ID, Status: domain.OpRunning}
	if a, ok := identity.ActorFrom(ctx); ok {
		r.TriggeredBy = a.String()
	}
	if r, err = c.store.CreateJobRun(ctx, r); err != nil {
		done()
		return domain.JobRun{}, err
	}
	// Not an operation Shutdown waits for: a job may run for hours, the
	// manager stops it instead.
	if err := c.mgr.GoLoop(func() {
		defer done()
		c.runJob(runCtx, project, svc, j, r)
	}); err != nil {
		done()
		r.Status, r.Error = domain.OpFailed, err.Error()
		_ = c.store.FinishJobRun(context.WithoutCancel(ctx), r)
		return domain.JobRun{}, err
	}
	return r, nil
}

func (c *Projects) runJob(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, r domain.JobRun) {
	log := c.log.With("job", j.ID, "run", r.ID, "project", project.Name, "service", svc.Name)
	var out = io.Discard
	if err := os.MkdirAll(c.jobLogDir(svc.ID), 0o700); err != nil {
		log.Error("cannot create job log", "err", err)
	} else if f, err := os.Create(c.jobLogPath(svc.ID, r.ID)); err != nil {
		log.Error("cannot create job log", "err", err)
	} else {
		defer f.Close()
		out = f
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, "[%s] "+format+"\n", append([]any{time.Now().UTC().Format(time.TimeOnly)}, args...)...)
	}

	start := time.Now()
	code, err := c.execJob(ctx, project, svc, j, r, out, logf)
	r.ExitCode = code
	r.Status = domain.OpSucceeded
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			err = fmt.Errorf("timed out after %s", time.Duration(j.TimeoutSec)*time.Second)
		case c.bg.Err() != nil:
			err = errors.New("interrupted by manager shutdown")
		case ctx.Err() != nil:
			err = errors.New("cancelled")
		}
		r.Status, r.Error = domain.OpFailed, err.Error()
		logf("Failed: %s", r.Error)
	} else {
		logf("Succeeded in %s", time.Since(start).Round(time.Millisecond))
	}
	bg := context.WithoutCancel(ctx)
	if err := c.store.FinishJobRun(bg, r); errors.Is(err, domain.ErrNotFound) || r.Error == "cancelled" {
		return // the job or its app is being deleted
	} else if err != nil {
		log.Error("cannot record job run", "err", err)
	}
	c.pruneJobRuns(bg, svc.ID, j.ID)

	name := project.Name + "/" + svc.Name + " " + j.Name
	e := notify.Event{
		Type:  domain.EventJobSucceeded,
		Level: notify.Success,
		Title: "Job " + name + " succeeded",
		Fields: []notify.Field{
			{Name: "Project", Value: project.Name},
			{Name: "Service", Value: svc.Name},
			{Name: "Job", Value: j.Name},
			{Name: "Duration", Value: time.Since(start).Round(time.Second).String()},
		},
	}
	key := ""
	if err != nil {
		e.Type, e.Level, e.Title, e.Message = domain.EventJobFailed, notify.Error, "Job "+name+" failed", r.Error
		key = "job:" + j.ID // a job failing every minute pages once
		log.Warn("job failed", "err", err)
	}
	c.mgr.Notify(e, "/services/"+svc.ID+"?tab=jobs", key)
}

// execJob runs the command in a one-off container of the current
// deployment: its pinned image, env, networks and volumes, like the
// pre-deploy command. It returns the exit code, nil if it never exited.
func (c *Projects) execJob(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, r domain.JobRun,
	out io.Writer, logf func(string, ...any)) (*int, error) {

	dk, id, err := c.mgr.StartJobContainer(ctx, project, svc, j, r.ID, logf)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dk.RemoveContainer(context.WithoutCancel(ctx), id, domain.StopTimeout) }()

	var code *int
	if status, werr := dk.WaitContainer(ctx, id); werr == nil {
		n := int(status)
		code = &n
	} else {
		err = werr
	}
	if code == nil { // timed out or cancelled: stop it before reading its logs
		_ = dk.StopContainer(context.WithoutCancel(ctx), id, nil)
	}
	if rc, lerr := dk.Logs(context.WithoutCancel(ctx), id, domain.LogOptions{Tail: jobLogLines}); lerr == nil {
		_, _ = io.Copy(out, rc)
		rc.Close()
	}
	switch {
	case code == nil:
		return nil, err
	case *code != 0:
		return code, fmt.Errorf("exited with code %d", *code)
	}
	return code, nil
}

// pruneJobRuns deletes a job's runs beyond the newest jobRunsKept, with
// their logs.
func (c *Projects) pruneJobRuns(ctx context.Context, serviceID, jobID string) {
	runs, err := c.store.ListJobRuns(ctx, jobID, 0)
	if err != nil {
		return
	}
	for i := jobRunsKept; i < len(runs); i++ {
		if c.store.DeleteJobRun(ctx, runs[i].ID) == nil {
			_ = os.Remove(c.jobLogPath(serviceID, runs[i].ID))
		}
	}
}

// RemoveStaleJobContainers removes job containers no run in flight owns,
// e.g. left by a manager that crashed.
func (c *Projects) RemoveStaleJobContainers(ctx context.Context, dk domain.ContainerRuntime, cts []domain.Container) {
	c.jobs.mu.Lock()
	owned := map[string]bool{}
	for id := range c.jobs.running {
		owned[id] = true
	}
	c.jobs.mu.Unlock()
	for _, ct := range cts {
		if ct.Labels[domain.LabelComponent] != deploy.JobComponent || owned[ct.Labels[domain.LabelJob]] {
			continue
		}
		c.log.Info("reconcile: removing stale job container", "container", ct.Names[0][1:])
		_ = dk.RemoveContainer(ctx, ct.ID, domain.StopTimeout)
	}
}

// JobRunningIn reports whether a job of the project is running: it keeps
// the project awake.
func (c *Projects) JobRunningIn(projectID string) bool {
	c.jobs.mu.Lock()
	defer c.jobs.mu.Unlock()
	for _, r := range c.jobs.running {
		if r.projectID == projectID {
			return true
		}
	}
	return false
}

func (c *Projects) jobLogDir(serviceID string) string {
	return filepath.Join(c.cfg.DataDir, "jobs", serviceID)
}

func (c *Projects) jobLogPath(serviceID, runID string) string {
	return filepath.Join(c.jobLogDir(serviceID), runID+".log")
}

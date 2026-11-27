package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
)

const (
	// jobRunsKept is how many runs (and logs) each job keeps.
	jobRunsKept = 50
	// jobLogLines is how much of a run's output its log keeps.
	jobLogLines = "2000"
)

// jobsState tracks the runs in flight, in memory: a job runs once at a time.
type jobsState struct {
	mu      sync.Mutex
	running map[string]jobRunning // job ID ->
}

type jobRunning struct {
	serviceID, projectID string
	cancel               context.CancelFunc
}

type JobView struct {
	domain.Job
	NextRun *time.Time     `json:"nextRun,omitempty"`
	LastRun *domain.JobRun `json:"lastRun,omitempty"`
}

// reloadJobs replaces the jobs' cron entries from the store.
func (c *Core) reloadJobs(ctx context.Context) error {
	js, err := c.store.ListJobs(ctx, "")
	if err != nil {
		return err
	}
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron == nil {
		return nil
	}
	for id, entry := range c.sched.jobs {
		c.sched.cron.Remove(entry)
		delete(c.sched.jobs, id)
	}
	for _, j := range js {
		if !j.Enabled {
			continue
		}
		entry, err := c.sched.cron.AddFunc(j.Cron, func() {
			_, err := c.startJobRun(c.bg, j.ID)
			switch {
			case errors.Is(err, ErrBusy) || errors.Is(err, ErrInvalid): // still running, stopped, not deployed
				c.log.Info("scheduled job skipped", "job", j.ID, "name", j.Name, "reason", err)
			case err != nil && !errors.Is(err, ErrShuttingDown):
				c.log.Warn("scheduled job did not start", "job", j.ID, "name", j.Name, "err", err)
			}
		})
		if err != nil {
			c.log.Error("invalid job schedule", "job", j.ID, "cron", j.Cron, "err", err)
			continue
		}
		c.sched.jobs[j.ID] = entry
	}
	return nil
}

func (c *Core) ListJobs(ctx context.Context, serviceID string) ([]JobView, error) {
	if _, err := c.store.GetService(ctx, serviceID); err != nil {
		return nil, err
	}
	js, err := c.store.ListJobs(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	views := make([]JobView, len(js))
	for i, j := range js {
		if views[i], err = c.jobView(ctx, j); err != nil {
			return nil, err
		}
	}
	return views, nil
}

func (c *Core) GetJob(ctx context.Context, id string) (JobView, error) {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

func (c *Core) jobView(ctx context.Context, j domain.Job) (JobView, error) {
	v := JobView{Job: j}
	runs, err := c.store.ListJobRuns(ctx, j.ID, 1)
	if err != nil {
		return JobView{}, err
	}
	if len(runs) > 0 {
		v.LastRun = &runs[0]
	}
	if !j.Enabled {
		return v, nil
	}
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if entry, ok := c.sched.jobs[j.ID]; ok && c.sched.cron != nil {
		if next := c.sched.cron.Entry(entry).Next; !next.IsZero() {
			v.NextRun = &next
		}
	} else if sched, err := data.CronParser.Parse(j.Cron); err == nil {
		next := sched.Next(time.Now().UTC())
		v.NextRun = &next
	}
	return v, nil
}

func (c *Core) CreateJob(ctx context.Context, serviceID string, in deploy.JobInput) (JobView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return JobView{}, err
	}
	if svc.Kind != domain.ServiceKindApp {
		return JobView{}, fmt.Errorf("%w: only apps run jobs", ErrInvalid)
	}
	if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
		return JobView{}, err
	}
	j := domain.Job{ServiceID: serviceID}
	if err := deploy.ApplyJobInput(&j, in); err != nil {
		return JobView{}, err
	}
	if j, err = c.store.CreateJob(ctx, j); err != nil {
		return JobView{}, jobConflict(err, in.Name)
	}
	if err := c.reloadJobs(ctx); err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

func (c *Core) UpdateJob(ctx context.Context, id string, in deploy.JobInput) (JobView, error) {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	if err := c.checkJobGitOwned(ctx, j); err != nil {
		return JobView{}, err
	}
	if err := deploy.ApplyJobInput(&j, in); err != nil {
		return JobView{}, err
	}
	if j, err = c.store.UpdateJob(ctx, j); err != nil {
		return JobView{}, jobConflict(err, in.Name)
	}
	if err := c.reloadJobs(ctx); err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

// DeleteJob stops its run, if any, and removes its runs' logs.
func (c *Core) DeleteJob(ctx context.Context, id string) error {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if err := c.checkJobGitOwned(ctx, j); err != nil {
		return err
	}
	if err := c.deleteJob(ctx, j); err != nil {
		return err
	}
	return c.reloadJobs(ctx)
}

// checkJobGitOwned refuses changes to the jobs of a git project: they are
// in its compose file.
func (c *Core) checkJobGitOwned(ctx context.Context, j domain.Job) error {
	svc, err := c.store.GetService(ctx, j.ServiceID)
	if err != nil {
		return err
	}
	return c.checkGitOwned(ctx, svc.ProjectID)
}

// deleteJob deletes j and its runs, stopping the one in progress; the
// caller reloads the schedule.
func (c *Core) deleteJob(ctx context.Context, j domain.Job) error {
	runs, err := c.store.ListJobRuns(ctx, j.ID, 0)
	if err != nil {
		return err
	}
	if err := c.store.DeleteJob(ctx, j.ID); err != nil {
		return err
	}
	c.cancelJobRuns(func(jobID string, _ jobRunning) bool { return jobID == j.ID })
	for _, r := range runs {
		_ = os.Remove(c.jobLogPath(j.ServiceID, r.ID))
	}
	return nil
}

// forgetServiceJobs stops the runs of a deleted service's jobs (the store
// deleted them with it) and removes their logs.
func (c *Core) forgetServiceJobs(serviceID string) {
	c.cancelJobRuns(func(_ string, r jobRunning) bool { return r.serviceID == serviceID })
	if err := os.RemoveAll(c.jobLogDir(serviceID)); err != nil {
		c.log.Warn("cannot remove job logs", "service", serviceID, "err", err)
	}
	if err := c.reloadJobs(c.bg); err != nil {
		c.log.Warn("cannot reload jobs", "err", err)
	}
}

// cancelJobRuns cancels the matching runs and returns their job IDs.
func (c *Core) cancelJobRuns(match func(jobID string, r jobRunning) bool) (ids []string) {
	c.jobs.mu.Lock()
	defer c.jobs.mu.Unlock()
	for id, r := range c.jobs.running {
		if match(id, r) {
			r.cancel()
			ids = append(ids, id)
		}
	}
	return ids
}

// removeJobContainers stops the service's runs before its volumes are
// removed: their containers hold them. A cancelled run removes its container;
// one that doesn't in time is removed here.
func (c *Core) removeJobContainers(ctx context.Context, dk *docker.Client, serviceID string) error {
	match := func(_ string, r jobRunning) bool { return r.serviceID == serviceID }
	ids := c.cancelJobRuns(match)
	for deadline := time.Now().Add(domain.StopTimeout + 5*time.Second); len(c.cancelJobRuns(match)) > 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
	}
	for _, id := range ids {
		cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelJob: id})
		if err != nil {
			return err
		}
		for _, ct := range cts {
			if err := dk.RemoveContainer(ctx, ct.ID, domain.StopTimeout); err != nil {
				return err
			}
		}
	}
	return nil
}

func jobConflict(err error, name string) error {
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: the service already has a job named %s", ErrInvalid, name)
	}
	return err
}

// RunJob runs a job now, unless it is running already.
func (c *Core) RunJob(ctx context.Context, id string) (domain.JobRun, error) {
	return c.startJobRun(ctx, id)
}

func (c *Core) ListJobRuns(ctx context.Context, jobID string) ([]domain.JobRun, error) {
	if _, err := c.store.GetJob(ctx, jobID); err != nil {
		return nil, err
	}
	return c.store.ListJobRuns(ctx, jobID, 0)
}

// JobRunLog opens a run's log; a missing log yields an empty reader.
func (c *Core) JobRunLog(ctx context.Context, jobID, runID string) (io.ReadCloser, error) {
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

func (c *Core) startJobRun(ctx context.Context, id string) (domain.JobRun, error) {
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
	if err := c.goLoop(func() {
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

func (c *Core) runJob(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, r domain.JobRun) {
	log := c.log.With("job", j.ID, "run", r.ID, "project", project.Name, "service", svc.Name)
	var out io.Writer = io.Discard
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
	c.Notify(e, "/services/"+svc.ID+"?tab=jobs", key)
}

// execJob runs the command in a one-off container of the current
// deployment: its pinned image, env, networks and volumes, like the
// pre-deploy command. It returns the exit code, nil if it never exited.
func (c *Core) execJob(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, r domain.JobRun,
	out io.Writer, logf func(string, ...any)) (*int, error) {

	dk, id, err := c.StartJobContainer(ctx, project, svc, j, r.ID, logf)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dk.RemoveContainer(context.WithoutCancel(ctx), id, domain.StopTimeout) }()

	wait := dk.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code *int
	select {
	case res := <-wait.Result:
		n := int(res.StatusCode)
		code = &n
	case err = <-wait.Error:
	}
	if code == nil { // timed out or cancelled: stop it before reading its logs
		_, _ = dk.ContainerStop(context.WithoutCancel(ctx), id, client.ContainerStopOptions{})
	}
	if rc, lerr := dk.ContainerLogs(context.WithoutCancel(ctx), id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: jobLogLines,
	}); lerr == nil {
		_, _ = stdcopy.StdCopy(out, out, rc)
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
func (c *Core) pruneJobRuns(ctx context.Context, serviceID, jobID string) {
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

// removeStaleJobContainers removes job containers no run in flight owns,
// e.g. left by a manager that crashed.
func (c *Core) removeStaleJobContainers(ctx context.Context, dk *docker.Client, cts []container.Summary) {
	c.jobs.mu.Lock()
	owned := map[string]bool{}
	for id := range c.jobs.running {
		owned[id] = true
	}
	c.jobs.mu.Unlock()
	for _, ct := range cts {
		if ct.Labels[docker.LabelComponent] != deploy.JobComponent || owned[ct.Labels[docker.LabelJob]] {
			continue
		}
		c.log.Info("reconcile: removing stale job container", "container", ct.Names[0][1:])
		_ = dk.RemoveContainer(ctx, ct.ID, domain.StopTimeout)
	}
}

// jobRunningIn reports whether a job of the project is running: it keeps
// the project awake.
func (c *Core) jobRunningIn(projectID string) bool {
	c.jobs.mu.Lock()
	defer c.jobs.mu.Unlock()
	for _, r := range c.jobs.running {
		if r.projectID == projectID {
			return true
		}
	}
	return false
}

func (c *Core) jobLogDir(serviceID string) string {
	return filepath.Join(c.cfg.DataDir, "jobs", serviceID)
}

func (c *Core) jobLogPath(serviceID, runID string) string {
	return filepath.Join(c.jobLogDir(serviceID), runID+".log")
}

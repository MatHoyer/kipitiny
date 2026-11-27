package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
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

// ReloadJobs replaces the jobs' cron entries from the store.
func (c *Projects) ReloadJobs(ctx context.Context) error {
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

func (c *Projects) ListJobs(ctx context.Context, serviceID string) ([]JobView, error) {
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

func (c *Projects) GetJob(ctx context.Context, id string) (JobView, error) {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

func (c *Projects) jobView(ctx context.Context, j domain.Job) (JobView, error) {
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

func (c *Projects) CreateJob(ctx context.Context, serviceID string, in deploy.JobInput) (JobView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return JobView{}, err
	}
	if svc.Kind != domain.ServiceKindApp {
		return JobView{}, fmt.Errorf("%w: only apps run jobs", ErrInvalid)
	}
	if err := c.CheckGitOwned(ctx, svc.ProjectID); err != nil {
		return JobView{}, err
	}
	j := domain.Job{ServiceID: serviceID}
	if err := deploy.ApplyJobInput(&j, in); err != nil {
		return JobView{}, err
	}
	if j, err = c.store.CreateJob(ctx, j); err != nil {
		return JobView{}, jobConflict(err, in.Name)
	}
	if err := c.ReloadJobs(ctx); err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

func (c *Projects) UpdateJob(ctx context.Context, id string, in deploy.JobInput) (JobView, error) {
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
	if err := c.ReloadJobs(ctx); err != nil {
		return JobView{}, err
	}
	return c.jobView(ctx, j)
}

// DeleteJob stops its run, if any, and removes its runs' logs.
func (c *Projects) DeleteJob(ctx context.Context, id string) error {
	j, err := c.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if err := c.checkJobGitOwned(ctx, j); err != nil {
		return err
	}
	if err := c.RemoveJob(ctx, j); err != nil {
		return err
	}
	return c.ReloadJobs(ctx)
}

// checkJobGitOwned refuses changes to the jobs of a git project: they are
// in its compose file.
func (c *Projects) checkJobGitOwned(ctx context.Context, j domain.Job) error {
	svc, err := c.store.GetService(ctx, j.ServiceID)
	if err != nil {
		return err
	}
	return c.CheckGitOwned(ctx, svc.ProjectID)
}

// RemoveJob deletes j and its runs, stopping the one in progress; the
// caller reloads the schedule.
func (c *Projects) RemoveJob(ctx context.Context, j domain.Job) error {
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

// ForgetServiceJobs stops the runs of a deleted service's jobs (the store
// deleted them with it) and removes their logs.
func (c *Projects) ForgetServiceJobs(serviceID string) {
	c.cancelJobRuns(func(_ string, r jobRunning) bool { return r.serviceID == serviceID })
	if err := os.RemoveAll(c.jobLogDir(serviceID)); err != nil {
		c.log.Warn("cannot remove job logs", "service", serviceID, "err", err)
	}
	if err := c.ReloadJobs(c.bg); err != nil {
		c.log.Warn("cannot reload jobs", "err", err)
	}
}

// cancelJobRuns cancels the matching runs and returns their job IDs.
func (c *Projects) cancelJobRuns(match func(jobID string, r jobRunning) bool) (ids []string) {
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

// RemoveJobContainers stops the service's runs before its volumes are
// removed: their containers hold them. A cancelled run removes its container;
// one that doesn't in time is removed here.
func (c *Projects) RemoveJobContainers(ctx context.Context, dk *docker.Client, serviceID string) error {
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

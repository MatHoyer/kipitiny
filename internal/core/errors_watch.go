package core

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/MatHoyer/kipitiny/internal/analytics"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	errorsWatchEvery = 30 * time.Second
	// spikeAfter is how long a deploy runs before its errors are compared
	// with the release before; spikeWindow how much of it is compared.
	spikeAfter  = 10 * time.Minute
	spikeWindow = time.Hour
	// A spike is at least spikeEvents events, spikeFactor times the
	// previous release's rate.
	spikeEvents = 10
	spikeFactor = 3
)

// DeployErrors compares the errors of an app's current deployment, over its
// first hour, with the release it replaced, over the day before.
type DeployErrors struct {
	DeploymentID string `json:"deploymentId"`
	Release      string `json:"release"`
	// Minutes of the deployment compared (up to 60).
	Minutes   float64 `json:"minutes"`
	Events    int     `json:"events"`
	NewIssues int     `json:"newIssues"`
	// Previous is the deployment before with another release, empty when
	// there is none; Rate and PreviousRate are events a minute.
	Previous        string  `json:"previous,omitempty"`
	PreviousRelease string  `json:"previousRelease,omitempty"`
	Rate            float64 `json:"rate"`
	PreviousRate    float64 `json:"previousRate"`
	// Spike: the deployment ran spikeAfter and raises errors well above the
	// release before.
	Spike bool `json:"spike"`
}

// ServiceDeployErrors is DeployErrors of the app's current deployment.
func (c *Core) ServiceDeployErrors(ctx context.Context, serviceID string) (DeployErrors, error) {
	svc, cl, _, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return DeployErrors{}, err
	}
	return c.deployErrors(ctx, cl, svc, time.Now())
}

func (c *Core) deployErrors(ctx context.Context, cl *analytics.Client, svc store.Service, now time.Time) (DeployErrors, error) {
	deps, err := c.store.ListDeployments(ctx, svc.ID, 20)
	if err != nil {
		return DeployErrors{}, err
	}
	var cur, prev *store.Deployment
	for i := range deps {
		d := &deps[i]
		if d.Status != store.DeploymentSucceeded || d.FinishedAt == nil {
			continue
		}
		if cur == nil {
			if svc.CurrentDeploymentID != "" && d.ID != svc.CurrentDeploymentID {
				continue
			}
			cur = d
		} else if ImageRelease(d.Image) != ImageRelease(cur.Image) {
			prev = d
			break
		}
	}
	if cur == nil {
		return DeployErrors{}, nil
	}
	start := *cur.FinishedAt
	end := start.Add(spikeWindow)
	if now.Before(end) {
		end = now
	}
	out := DeployErrors{DeploymentID: cur.ID, Release: ImageRelease(cur.Image), Minutes: end.Sub(start).Minutes()}
	re, err := cl.ReleaseErrors(ctx, svc.ErrorsProject, out.Release, start, end.Add(time.Second))
	if err != nil {
		return out, analyticsErr(err, "release")
	}
	out.Events, out.NewIssues = re.Events, re.NewIssues
	if out.Minutes > 0 {
		out.Rate = float64(out.Events) / out.Minutes
	}
	if prev != nil {
		out.Previous, out.PreviousRelease = prev.ID, ImageRelease(prev.Image)
		from := start.Add(-24 * time.Hour)
		if prev.FinishedAt.After(from) {
			from = *prev.FinishedAt
		}
		pre, err := cl.ReleaseErrors(ctx, svc.ErrorsProject, out.PreviousRelease, from, start)
		if err != nil {
			return out, analyticsErr(err, "release")
		}
		if m := start.Sub(from).Minutes(); m > 0 {
			out.PreviousRate = float64(pre.Events) / m
		}
	}
	out.Spike = now.Sub(start) >= spikeAfter && out.Events >= spikeEvents && out.Rate >= spikeFactor*out.PreviousRate
	return out, nil
}

// StartErrorsWatch alerts about new and regressed issues, and deploys
// raising errors, every 30 seconds.
func (c *Core) StartErrorsWatch() error {
	return c.goLoop(func() {
		tick := time.NewTicker(errorsWatchEvery)
		defer tick.Stop()
		for {
			if err := c.watchErrors(c.bg, time.Now()); err != nil && c.bg.Err() == nil {
				c.log.Debug("errors watch", "err", err)
			}
			select {
			case <-c.bg.Done():
				return
			case <-tick.C:
			}
		}
	})
}

func (c *Core) watchErrors(ctx context.Context, now time.Time) error {
	st := c.analyticsState(ctx)
	if !st.Enabled {
		return nil
	}
	cl, err := c.analyticsClient(ctx, st.Token)
	if err != nil {
		return err
	}
	return c.alertErrors(ctx, cl, now)
}

// alertErrors sends the issues opened or reopened since the last look, and
// the deploys raising errors not alerted about yet.
func (c *Core) alertErrors(ctx context.Context, cl *analytics.Client, now time.Time) error {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	byProject := map[int64]store.Service{}
	for _, s := range svcs {
		if s.Errors && s.ErrorsProject != 0 {
			byProject[s.ErrorsProject] = s
		}
	}
	if len(byProject) == 0 {
		return nil
	}
	projects := map[string]store.Project{}
	project := func(id string) store.Project {
		if p, ok := projects[id]; ok {
			return p
		}
		p, _ := c.store.GetProject(ctx, id)
		projects[id] = p
		return p
	}

	// Issues opened or reopened since the last look; none the first time,
	// so a restart doesn't alert again.
	ch, err := cl.ErrorChanges(ctx, max(0, c.errorsSeq))
	if err != nil {
		return err
	}
	if c.errorsSeq < 0 {
		c.errorsSeq = ch.Last
	} else {
		for _, x := range ch.Changes {
			c.errorsSeq = x.Seq
			svc, ok := byProject[x.Project]
			if !ok || project(svc.ProjectID).ParentID != "" { // previews: no alerts
				continue
			}
			c.notifyIssue(project(svc.ProjectID), svc, x)
		}
	}

	for _, svc := range byProject {
		p := project(svc.ProjectID)
		if p.ParentID != "" {
			continue
		}
		de, err := c.deployErrors(ctx, cl, svc, now)
		if err != nil || !de.Spike || c.spiked[de.DeploymentID] {
			continue
		}
		c.spiked[de.DeploymentID] = true
		c.notifySpike(p, svc, de)
	}
	return nil
}

func (c *Core) notifyIssue(p store.Project, svc store.Service, x analytics.ErrorChange) {
	e := notify.Event{Type: EventErrorNew, Level: notify.Error, Title: fmt.Sprintf("New error in %s/%s", p.Name, svc.Name)}
	if x.Kind == "regression" {
		e.Type, e.Level, e.Title = EventErrorRegression, notify.Warning, fmt.Sprintf("Error back in %s/%s", p.Name, svc.Name)
	}
	e.Message = x.Title
	e.Fields = []notify.Field{{Name: "Where", Value: x.Culprit}, {Name: "Release", Value: x.Release}}
	c.notify(e, "/services/"+svc.ID+"?tab=errors&issue="+strconv.FormatInt(x.Issue, 10), "")
}

func (c *Core) notifySpike(p store.Project, svc store.Service, de DeployErrors) {
	msg := fmt.Sprintf("%d errors in the first %.0f minutes of %s (%.1f a minute", de.Events, de.Minutes, de.Release, de.Rate)
	if de.PreviousRelease != "" {
		msg += fmt.Sprintf(", %.1f before with %s", de.PreviousRate, de.PreviousRelease)
	}
	msg += fmt.Sprintf("), %d new issues. Roll back from its Deployments tab if it's the deploy's fault.", de.NewIssues)
	c.notify(notify.Event{
		Type: EventErrorSpike, Level: notify.Error, Title: fmt.Sprintf("Errors up after deploying %s/%s", p.Name, svc.Name),
		Message: msg, Fields: []notify.Field{{Name: "Project", Value: p.Name}, {Name: "Service", Value: svc.Name}},
	}, "/services/"+svc.ID+"?tab=deployments", "")
}

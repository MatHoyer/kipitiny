package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// errorsNextSetting is the next DSN project number: never reused, so a new
// app doesn't see a deleted one's issues.
const errorsNextSetting = "errors_next_project"

// errorsIdentity gives svc its DSN the first time error tracking is on.
func (c *Core) errorsIdentity(ctx context.Context, svc *domain.Service) error {
	if !svc.Errors || svc.ErrorsProject != 0 {
		return nil
	}
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()
	next := int64(1)
	if v, err := c.store.GetSetting(ctx, errorsNextSetting); err == nil && v != "" {
		if next, err = strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("%s: %w", errorsNextSetting, err)
		}
	}
	if err := c.store.SetSetting(ctx, errorsNextSetting, strconv.FormatInt(next+1, 10)); err != nil {
		return err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	svc.ErrorsProject, svc.ErrorsKey = next, hex.EncodeToString(b)
	return nil
}

// errorsDSN is svc's DSN, on the analytics domain.
func errorsDSN(domain string, svc domain.Service) string {
	return "https://" + svc.ErrorsKey + "@" + domain + "/" + strconv.FormatInt(svc.ErrorsProject, 10)
}

// ImageRelease is the release an image's errors are reported under: its
// tag, else the start of its digest.
func ImageRelease(image string) string {
	ref, digest, _ := strings.Cut(image, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[i+1:]
	}
	if d := strings.TrimPrefix(digest, "sha256:"); d != "" {
		return d[:min(12, len(d))]
	}
	return "latest"
}

// errorsEnv is what a deploy adds to svc's environment for its Sentry SDK
// (SENTRY_RELEASE is added by containerSpec, from the image deployed);
// variables the app sets itself win.
func (c *Core) errorsEnv(ctx context.Context, project domain.Project, svc domain.Service) map[string]string {
	if !svc.Errors || svc.ErrorsKey == "" {
		return nil
	}
	a := c.analyticsState(ctx)
	if !a.Enabled {
		return nil
	}
	env := "production"
	if project.ParentID != "" {
		env = "preview-" + project.Preview.Name
	}
	return map[string]string{"SENTRY_DSN": errorsDSN(a.Domain, svc), "SENTRY_ENVIRONMENT": env}
}

// syncErrorProjects sends the analytics server the apps taking errors, when
// they changed since the last time (or the container is new).
func (c *Core) syncErrorProjects(ctx context.Context, id, token string) error {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	ps := []analytics.ErrorProject{}
	for _, s := range svcs {
		if s.Errors && s.ErrorsKey != "" {
			ps = append(ps, analytics.ErrorProject{ID: s.ErrorsProject, Key: s.ErrorsKey})
		}
	}
	slices.SortFunc(ps, func(a, b analytics.ErrorProject) int { return int(a.ID - b.ID) })
	key := id
	for _, p := range ps {
		key += " " + strconv.FormatInt(p.ID, 10) + ":" + p.Key
	}
	if key == c.analyticsErrors {
		return nil
	}
	cl, err := c.analyticsClient(ctx, token)
	if err != nil {
		return err
	}
	if err := cl.SetErrorProjects(ctx, ps); err != nil {
		return err
	}
	c.analyticsErrors = key
	return nil
}

// serviceErrors checks the app's error tracking is on and returns the
// analytics server's client.
func (c *Core) serviceErrors(ctx context.Context, serviceID string) (domain.Service, *analytics.Client, analyticsState, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return svc, nil, analyticsState{}, err
	}
	if !svc.Errors || svc.ErrorsProject == 0 {
		return svc, nil, analyticsState{}, fmt.Errorf("%w: error tracking is off for %s (its settings)", ErrInvalid, svc.Name)
	}
	cl, st, err := c.analyticsOn(ctx)
	return svc, cl, st, err
}

// ErrorsQuery lists an app's issues: Status open, resolved or ignored (all
// when empty), Search in titles and culprits, Sort lastSeen, firstSeen,
// events or users.
type ErrorsQuery struct {
	Status, Search, Sort string
	Limit, Offset        int
}

// ErrorsView is a page of an app's issues, and its DSN.
type ErrorsView struct {
	analytics.IssueList
	DSN string `json:"dsn"`
}

func (c *Core) ServiceErrors(ctx context.Context, serviceID string, q ErrorsQuery) (ErrorsView, error) {
	svc, cl, st, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return ErrorsView{}, err
	}
	l, err := cl.Issues(ctx, analytics.IssueQuery{
		Project: svc.ErrorsProject, Status: q.Status, Search: q.Search, Sort: q.Sort, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil {
		return ErrorsView{}, analyticsErr(err, "issues")
	}
	return ErrorsView{IssueList: l, DSN: errorsDSN(st.Domain, svc)}, nil
}

// ServiceErrorSeries counts the app's events in [from, to), in buckets of
// interval seconds (whole minutes).
func (c *Core) ServiceErrorSeries(ctx context.Context, serviceID string, from, to time.Time, interval int) ([]analytics.ErrorPoint, error) {
	svc, cl, _, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	ps, err := cl.ErrorSeries(ctx, svc.ErrorsProject, from, to, time.Duration(interval)*time.Second)
	return ps, analyticsErr(err, "series")
}

func (c *Core) ServiceIssue(ctx context.Context, serviceID string, issue int64) (analytics.Issue, error) {
	svc, cl, _, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return analytics.Issue{}, err
	}
	is, err := cl.Issue(ctx, svc.ErrorsProject, issue)
	return is, analyticsErr(err, fmt.Sprintf("issue %d", issue))
}

// ServiceIssueEvent is one of the issue's kept events, its latest when
// event is empty.
func (c *Core) ServiceIssueEvent(ctx context.Context, serviceID string, issue int64, event string) (analytics.IssueEvent, error) {
	svc, cl, _, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return analytics.IssueEvent{}, err
	}
	ev, err := cl.Event(ctx, svc.ErrorsProject, issue, event)
	return ev, analyticsErr(err, fmt.Sprintf("event of issue %d", issue))
}

// SetServiceIssueStatus resolves, ignores or reopens an issue. A resolved
// one reopens when it happens again.
func (c *Core) SetServiceIssueStatus(ctx context.Context, serviceID string, issue int64, status string) (analytics.Issue, error) {
	svc, cl, _, err := c.serviceErrors(ctx, serviceID)
	if err != nil {
		return analytics.Issue{}, err
	}
	is, err := cl.SetIssueStatus(ctx, svc.ErrorsProject, issue, status)
	return is, analyticsErr(err, fmt.Sprintf("issue %d", issue))
}

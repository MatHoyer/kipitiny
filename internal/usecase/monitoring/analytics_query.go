package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// AnalyticsQuery is a range of an app's stats: [From, To), in buckets of
// Interval seconds aligned with Offset (seconds east of UTC), narrowed by
// "<breakdown>:<value>" filters.
type AnalyticsQuery struct {
	From, To time.Time
	Interval int
	Offset   int
	Filters  []string
}

// AnalyticsView is an app's stats, and the snippet its pages include.
type AnalyticsView struct {
	analytics.Stats
	Script string `json:"script"`
}

// serviceAnalytics checks the app's analytics are on and returns its
// domain's client.
func (c *Monitoring) serviceAnalytics(ctx context.Context, serviceID string) (domain.Service, *analytics.Client, AnalyticsState, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return svc, nil, AnalyticsState{}, err
	}
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return svc, nil, st, err
	}
	if !svc.Analytics || svc.Domain == "" {
		return svc, nil, st, fmt.Errorf("%w: analytics are off for %s (its settings)", domain.ErrInvalid, svc.Name)
	}
	return svc, cl, st, nil
}

// analyticsOn returns the server's client while analytics are on.
func (c *Monitoring) analyticsOn(ctx context.Context) (*analytics.Client, AnalyticsState, error) {
	st := c.analyticsState(ctx)
	if !st.Enabled {
		return nil, st, fmt.Errorf("%w: analytics are off (Settings › Analytics)", domain.ErrInvalid)
	}
	cl, err := c.analyticsClient(ctx, st.Token)
	return cl, st, err
}

// analyticsErr turns the server's refusals into core's errors; what names
// what wasn't found.
func analyticsErr(err error, what string) error {
	switch {
	case errors.Is(err, analytics.ErrInvalid):
		return fmt.Errorf("%w: %s", domain.ErrInvalid, strings.TrimPrefix(err.Error(), analytics.ErrInvalid.Error()+": "))
	case errors.Is(err, analytics.ErrNotFound):
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	return err
}

// Since keeps q to what svc collected: a hostname it took over brings no
// earlier visits.
func (q AnalyticsQuery) Since(svc domain.Service) AnalyticsQuery {
	if q.From.Before(svc.AnalyticsSince) {
		q.From = svc.AnalyticsSince
	}
	if !q.To.IsZero() && q.To.Before(q.From) {
		q.To = q.From
	}
	return q
}

func (q AnalyticsQuery) query(hosts ...string) (analytics.Query, error) {
	filters, err := analytics.ParseFilters(q.Filters)
	if err != nil {
		return analytics.Query{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	return analytics.Query{
		Hosts: hosts, From: q.From, To: q.To,
		Interval: time.Duration(q.Interval) * time.Second, Offset: q.Offset, Filters: filters,
	}, nil
}

func (c *Monitoring) ServiceAnalytics(ctx context.Context, serviceID string, q AnalyticsQuery) (AnalyticsView, error) {
	svc, cl, st, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return AnalyticsView{}, err
	}
	aq, err := q.Since(svc).query(svc.Domain)
	if err != nil {
		return AnalyticsView{}, err
	}
	s, err := cl.Stats(ctx, aq)
	if err != nil {
		return AnalyticsView{}, err
	}
	return AnalyticsView{Stats: s, Script: serviceScript(svc, st.Domain)}, nil
}

// ServiceVisits lists the app's visits started in q's range, newest first:
// limit (at most 100) of them, started before the visit before when set.
func (c *Monitoring) ServiceVisits(ctx context.Context, serviceID string, q AnalyticsQuery, before string, limit int) ([]analytics.Visit, error) {
	svc, cl, _, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	q.Interval = 86400 // only checked by the server
	aq, err := q.Since(svc).query(svc.Domain)
	if err != nil {
		return nil, err
	}
	return cl.Visits(ctx, aq, before, limit)
}

// ServiceFunnel counts the app's visits of q's range through steps, in
// order ("page:<path>" or "event:<name>", 2 to 8); q's filters aside.
func (c *Monitoring) ServiceFunnel(ctx context.Context, serviceID string, q AnalyticsQuery, steps []string) ([]analytics.FunnelStep, error) {
	svc, cl, _, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if _, err := analytics.ParseSteps(steps); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	q.Interval, q.Filters = 86400, nil // the interval is only checked by the server
	aq, err := q.Since(svc).query(svc.Domain)
	if err != nil {
		return nil, err
	}
	fs, err := cl.Funnel(ctx, aq, steps)
	return fs, analyticsErr(err, "funnel")
}

// ServiceVisit is one of the app's visits, with its journey.
func (c *Monitoring) ServiceVisit(ctx context.Context, serviceID, visitID string) (analytics.VisitDetail, error) {
	svc, cl, _, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return analytics.VisitDetail{}, err
	}
	d, err := cl.Visit(ctx, []string{svc.Domain}, visitID)
	if err == nil && d.StartedAt < svc.AnalyticsSince.Unix() {
		return analytics.VisitDetail{}, fmt.Errorf("%w: visit %s", domain.ErrNotFound, visitID)
	}
	return d, analyticsErr(err, "visit "+visitID)
}

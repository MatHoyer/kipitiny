package analytics

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// SetErrorProjects sends the apps taking errors.
func (c *Client) SetErrorProjects(ctx context.Context, ps []analytics.ErrorProject) error {
	if ps == nil {
		ps = []analytics.ErrorProject{}
	}
	return c.do(ctx, http.MethodPut, "/api/error-projects", ps, nil)
}

func (c *Client) Issues(ctx context.Context, q analytics.IssueQuery) (analytics.IssueList, error) {
	v := url.Values{"project": {strconv.FormatInt(q.Project, 10)}, "status": {q.Status}, "q": {q.Search}, "sort": {q.Sort},
		"limit": {strconv.Itoa(q.Limit)}, "offset": {strconv.Itoa(q.Offset)}}
	var l analytics.IssueList
	err := c.do(ctx, http.MethodGet, "/api/issues?"+v.Encode(), nil, &l)
	return l, err
}

func issuePath(project, id int64, suffix string) string {
	return "/api/issues/" + strconv.FormatInt(id, 10) + suffix + "?project=" + strconv.FormatInt(project, 10)
}

func (c *Client) Issue(ctx context.Context, project, id int64) (analytics.Issue, error) {
	var is analytics.Issue
	err := c.do(ctx, http.MethodGet, issuePath(project, id, ""), nil, &is)
	return is, err
}

func (c *Client) SetIssueStatus(ctx context.Context, project, id int64, status string) (analytics.Issue, error) {
	var is analytics.Issue
	err := c.do(ctx, http.MethodPut, issuePath(project, id, "/status"), map[string]string{"status": status}, &is)
	return is, err
}

// Event is one of the issue's kept events, its latest when event is empty.
func (c *Client) Event(ctx context.Context, project, id int64, event string) (analytics.IssueEvent, error) {
	var ev analytics.IssueEvent
	err := c.do(ctx, http.MethodGet, issuePath(project, id, "/event")+"&event="+url.QueryEscape(event), nil, &ev)
	return ev, err
}

func (c *Client) ErrorSeries(ctx context.Context, project int64, from, to time.Time, interval time.Duration) ([]analytics.ErrorPoint, error) {
	v := url.Values{}
	v.Set("project", strconv.FormatInt(project, 10))
	v.Set("from", strconv.FormatInt(from.Unix(), 10))
	v.Set("to", strconv.FormatInt(to.Unix(), 10))
	v.Set("interval", strconv.FormatInt(int64(interval.Seconds()), 10))
	var ps []analytics.ErrorPoint
	err := c.do(ctx, http.MethodGet, "/api/errors/series?"+v.Encode(), nil, &ps)
	return ps, err
}

// ErrorChanges are the issues opened or reopened after seq.
func (c *Client) ErrorChanges(ctx context.Context, after int64) (analytics.ErrorChanges, error) {
	var out analytics.ErrorChanges
	err := c.do(ctx, http.MethodGet, "/api/errors/changes?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

// ReleaseErrors counts project's events of release in [from, to).
func (c *Client) ReleaseErrors(ctx context.Context, project int64, release string, from, to time.Time) (analytics.ReleaseErrors, error) {
	v := url.Values{"release": {release}}
	v.Set("project", strconv.FormatInt(project, 10))
	v.Set("from", strconv.FormatInt(from.Unix(), 10))
	v.Set("to", strconv.FormatInt(to.Unix(), 10))
	var out analytics.ReleaseErrors
	err := c.do(ctx, http.MethodGet, "/api/errors/release?"+v.Encode(), nil, &out)
	return out, err
}

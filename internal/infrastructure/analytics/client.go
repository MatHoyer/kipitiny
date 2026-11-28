// Package analytics is the manager's client of the analytics server's
// private API.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// Client reads the analytics server's private API, from the manager.
type Client struct {
	BaseURL string // http://<container>:<PrivatePort>
	Token   string
	HTTP    *http.Client
}

// New is a client of the server at baseURL.
func New(baseURL, token string) *Client { return &Client{BaseURL: baseURL, Token: token} }

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		err := fmt.Errorf("analytics: %s: %s", res.Status, bytes.TrimSpace(msg))
		switch res.StatusCode {
		case http.StatusNotFound:
			err = fmt.Errorf("%w: %w", analytics.ErrNotFound, err)
		case http.StatusBadRequest:
			err = fmt.Errorf("%w: %s", analytics.ErrInvalid, bytes.TrimSpace(msg))
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// SetSites sends the tracked hostnames, and the paths of those with their
// own.
func (c *Client) SetSites(ctx context.Context, hosts []string, paths map[string]analytics.Paths) error {
	if hosts == nil {
		hosts = []string{}
	}
	return c.do(ctx, http.MethodPut, "/api/sites", map[string]any{"hostnames": hosts, "paths": paths}, nil)
}

func values(q analytics.Query) url.Values {
	v := url.Values{"host": q.Hosts}
	v.Set("from", strconv.FormatInt(q.From.Unix(), 10))
	v.Set("to", strconv.FormatInt(q.To.Unix(), 10))
	v.Set("interval", strconv.FormatInt(int64(q.Interval.Seconds()), 10))
	v.Set("offset", strconv.Itoa(q.Offset))
	for _, f := range q.Filters {
		v.Add("filter", f.Key+":"+f.Value)
	}
	return v
}

func (c *Client) Stats(ctx context.Context, q analytics.Query) (analytics.Stats, error) {
	var s analytics.Stats
	err := c.do(ctx, http.MethodGet, "/api/stats?"+values(q).Encode(), nil, &s)
	return s, err
}

// Visits lists q's visits (q.Interval only checked): see Store.Visits.
func (c *Client) Visits(ctx context.Context, q analytics.Query, before string, limit int) ([]analytics.Visit, error) {
	v := values(q)
	v.Set("before", before)
	v.Set("limit", strconv.Itoa(limit))
	var vs []analytics.Visit
	err := c.do(ctx, http.MethodGet, "/api/visits?"+v.Encode(), nil, &vs)
	return vs, err
}

func (c *Client) Visit(ctx context.Context, hosts []string, id string) (analytics.VisitDetail, error) {
	var d analytics.VisitDetail
	err := c.do(ctx, http.MethodGet, "/api/visits/"+url.PathEscape(id)+"?"+url.Values{"host": hosts}.Encode(), nil, &d)
	return d, err
}

// Funnel counts q's visits through steps ("page:<path>", "event:<name>").
func (c *Client) Funnel(ctx context.Context, q analytics.Query, steps []string) ([]analytics.FunnelStep, error) {
	v := values(q)
	v["step"] = steps
	var fs []analytics.FunnelStep
	err := c.do(ctx, http.MethodGet, "/api/funnel?"+v.Encode(), nil, &fs)
	return fs, err
}

func (c *Client) Trackers(ctx context.Context) ([]analytics.Tracker, error) {
	var ts []analytics.Tracker
	err := c.do(ctx, http.MethodGet, "/api/trackers", nil, &ts)
	return ts, err
}

func (c *Client) Tracker(ctx context.Context, id string) (analytics.Tracker, error) {
	var t analytics.Tracker
	err := c.do(ctx, http.MethodGet, "/api/trackers/"+url.PathEscape(id), nil, &t)
	return t, err
}

func (c *Client) CreateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	var out analytics.Tracker
	err := c.do(ctx, http.MethodPost, "/api/trackers", t, &out)
	return out, err
}

func (c *Client) UpdateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	var out analytics.Tracker
	err := c.do(ctx, http.MethodPut, "/api/trackers/"+url.PathEscape(t.ID), t, &out)
	return out, err
}

func (c *Client) DeleteTracker(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/trackers/"+url.PathEscape(id), nil, nil)
}

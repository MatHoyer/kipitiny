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
)

// Client reads the analytics server's private API, from the manager.
type Client struct {
	BaseURL string // http://<container>:<PrivatePort>
	Token   string
	HTTP    *http.Client
}

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
			err = fmt.Errorf("%w: %v", ErrNotFound, err)
		case http.StatusBadRequest:
			err = fmt.Errorf("%w: %s", ErrInvalid, bytes.TrimSpace(msg))
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *Client) SetSites(ctx context.Context, hosts []string) error {
	if hosts == nil {
		hosts = []string{}
	}
	return c.do(ctx, http.MethodPut, "/api/sites", map[string][]string{"hostnames": hosts}, nil)
}

func (q Query) values() url.Values {
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

func (c *Client) Stats(ctx context.Context, q Query) (Stats, error) {
	var s Stats
	err := c.do(ctx, http.MethodGet, "/api/stats?"+q.values().Encode(), nil, &s)
	return s, err
}

// Visits lists q's visits (q.Interval only checked): see Store.Visits.
func (c *Client) Visits(ctx context.Context, q Query, before string, limit int) ([]Visit, error) {
	v := q.values()
	v.Set("before", before)
	v.Set("limit", strconv.Itoa(limit))
	var vs []Visit
	err := c.do(ctx, http.MethodGet, "/api/visits?"+v.Encode(), nil, &vs)
	return vs, err
}

func (c *Client) Visit(ctx context.Context, hosts []string, id string) (VisitDetail, error) {
	var d VisitDetail
	err := c.do(ctx, http.MethodGet, "/api/visits/"+url.PathEscape(id)+"?"+url.Values{"host": hosts}.Encode(), nil, &d)
	return d, err
}

func (c *Client) Trackers(ctx context.Context) ([]Tracker, error) {
	var ts []Tracker
	err := c.do(ctx, http.MethodGet, "/api/trackers", nil, &ts)
	return ts, err
}

func (c *Client) Tracker(ctx context.Context, id string) (Tracker, error) {
	var t Tracker
	err := c.do(ctx, http.MethodGet, "/api/trackers/"+url.PathEscape(id), nil, &t)
	return t, err
}

func (c *Client) CreateTracker(ctx context.Context, t Tracker) (Tracker, error) {
	var out Tracker
	err := c.do(ctx, http.MethodPost, "/api/trackers", t, &out)
	return out, err
}

func (c *Client) UpdateTracker(ctx context.Context, t Tracker) (Tracker, error) {
	var out Tracker
	err := c.do(ctx, http.MethodPut, "/api/trackers/"+url.PathEscape(t.ID), t, &out)
	return out, err
}

func (c *Client) DeleteTracker(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/trackers/"+url.PathEscape(id), nil, nil)
}

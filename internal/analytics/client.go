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
		return fmt.Errorf("analytics: %s: %s", res.Status, bytes.TrimSpace(msg))
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

func (c *Client) Stats(ctx context.Context, q Query) (Stats, error) {
	v := url.Values{"host": q.Hosts}
	v.Set("from", strconv.FormatInt(q.From.Unix(), 10))
	v.Set("to", strconv.FormatInt(q.To.Unix(), 10))
	v.Set("interval", strconv.FormatInt(int64(q.Interval.Seconds()), 10))
	v.Set("offset", strconv.Itoa(q.Offset))
	var s Stats
	err := c.do(ctx, http.MethodGet, "/api/stats?"+v.Encode(), nil, &s)
	return s, err
}

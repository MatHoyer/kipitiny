// Package cloudflare is a small client for the parts of the Cloudflare API the
// manager uses: zones, DNS records and remotely-managed tunnel routes
// (network.DNS).
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	cf "github.com/kipit-labs/kipitiny/internal/domain/cloudflare"
)

type Client struct {
	token string
	hc    *http.Client
	base  string
}

// BaseURL is the Cloudflare API; tests point it at a fake.
var BaseURL = "https://api.cloudflare.com/client/v4"

func New(token string) *Client {
	return &Client{token: token, hc: &http.Client{Timeout: 20 * time.Second}, base: BaseURL}
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) (envelope, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return envelope{}, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return envelope{}, err
	}
	defer res.Body.Close()
	var env envelope
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&env); err != nil {
		return envelope{}, fmt.Errorf("cloudflare: %s %s: HTTP %d", method, path, res.StatusCode)
	}
	if !env.Success {
		e := &cf.APIError{Status: res.StatusCode}
		for _, m := range env.Errors {
			e.Messages = append(e.Messages, m.Message)
		}
		if len(e.Messages) == 0 {
			e.Messages = []string{http.StatusText(res.StatusCode)}
		}
		return env, e
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return env, fmt.Errorf("cloudflare: decode %s: %w", path, err)
		}
	}
	return env, nil
}

// list fetches every page of a paginated collection.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for page := 1; page <= 100; page++ {
		q.Set("page", fmt.Sprint(page))
		var items []T
		env, err := c.do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if env.ResultInfo.TotalPages <= page {
			break
		}
	}
	return all, nil
}

// Zones lists the zones the token can read.
func (c *Client) Zones(ctx context.Context) ([]cf.Zone, error) {
	return list[cf.Zone](ctx, c, "/zones", url.Values{"per_page": {"50"}})
}

// SSLMode returns the zone's SSL/TLS mode: off, flexible, full or strict.
func (c *Client) SSLMode(ctx context.Context, zoneID string) (string, error) {
	var s struct {
		Value string `json:"value"`
	}
	_, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/settings/ssl", nil, &s)
	return s.Value, err
}

// Records lists the records named name in a zone.
func (c *Client) Records(ctx context.Context, zoneID, name string) ([]cf.Record, error) {
	return list[cf.Record](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"name.exact": {name}, "per_page": {"100"}})
}

// ManagedRecords lists the records the manager owns in a zone.
func (c *Client) ManagedRecords(ctx context.Context, zoneID string) ([]cf.Record, error) {
	return list[cf.Record](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"comment.exact": {cf.ManagedComment}, "per_page": {"500"}})
}

func (c *Client) CreateRecord(ctx context.Context, zoneID string, r cf.Record) error {
	r.ID, r.Comment = "", cf.ManagedComment
	_, err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", r, nil)
	return err
}

func (c *Client) UpdateRecord(ctx context.Context, zoneID, id string, r cf.Record) error {
	r.ID, r.Comment = "", cf.ManagedComment
	_, err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+id, r, nil)
	return err
}

func (c *Client) DeleteRecord(ctx context.Context, zoneID, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+id, nil, nil)
	return err
}

// TunnelIngress returns the tunnel's routes and the rest of its configuration.
func (c *Client) TunnelIngress(ctx context.Context, t cf.Tunnel) ([]cf.Ingress, map[string]any, error) {
	var res struct {
		Config map[string]any `json:"config"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/accounts/"+t.AccountID+"/cfd_tunnel/"+t.ID+"/configurations", nil, &res); err != nil {
		return nil, nil, err
	}
	cfg := res.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	var rules []cf.Ingress
	raw, _ := cfg["ingress"].([]any)
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			rules = append(rules, m)
		}
	}
	return rules, cfg, nil
}

// SetTunnelIngress replaces the tunnel's routes, keeping the rest of cfg.
func (c *Client) SetTunnelIngress(ctx context.Context, t cf.Tunnel, cfg map[string]any, rules []cf.Ingress) error {
	cfg["ingress"] = rules
	_, err := c.do(ctx, http.MethodPut, "/accounts/"+t.AccountID+"/cfd_tunnel/"+t.ID+"/configurations", map[string]any{"config": cfg}, nil)
	return err
}

// Package cloudflare is a small client for the parts of the Cloudflare API the
// manager uses: zones, DNS records and remotely-managed tunnel routes.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ManagedComment marks the DNS records the manager owns; it never touches
// records without it.
const ManagedComment = "managed by kipitiny"

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

// APIError is an error reported by Cloudflare.
type APIError struct {
	Status   int
	Messages []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cloudflare: %s (HTTP %d)", strings.Join(e.Messages, "; "), e.Status)
}

// Forbidden reports whether err means the token lacks a permission.
func Forbidden(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Status == http.StatusUnauthorized)
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
		e := &APIError{Status: res.StatusCode}
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

type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// Zones lists the zones the token can read.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	return list[Zone](ctx, c, "/zones", url.Values{"per_page": {"50"}})
}

// ZoneFor returns the zone hosting name (the longest matching zone name).
func ZoneFor(zones []Zone, name string) (Zone, bool) {
	var best Zone
	for _, z := range zones {
		if (name == z.Name || strings.HasSuffix(name, "."+z.Name)) && len(z.Name) > len(best.Name) {
			best = z
		}
	}
	return best, best.ID != ""
}

// SSLMode returns the zone's SSL/TLS mode: off, flexible, full or strict.
func (c *Client) SSLMode(ctx context.Context, zoneID string) (string, error) {
	var s struct {
		Value string `json:"value"`
	}
	_, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/settings/ssl", nil, &s)
	return s.Value, err
}

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"` // 1 = automatic
	Comment string `json:"comment"`
}

// Managed reports whether the manager owns r.
func (r Record) Managed() bool { return r.Comment == ManagedComment }

// Records lists the records named name in a zone.
func (c *Client) Records(ctx context.Context, zoneID, name string) ([]Record, error) {
	return list[Record](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"name.exact": {name}, "per_page": {"100"}})
}

// ManagedRecords lists the records the manager owns in a zone.
func (c *Client) ManagedRecords(ctx context.Context, zoneID string) ([]Record, error) {
	return list[Record](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"comment.exact": {ManagedComment}, "per_page": {"500"}})
}

func (c *Client) CreateRecord(ctx context.Context, zoneID string, r Record) error {
	r.ID, r.Comment = "", ManagedComment
	_, err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", r, nil)
	return err
}

func (c *Client) UpdateRecord(ctx context.Context, zoneID, id string, r Record) error {
	r.ID, r.Comment = "", ManagedComment
	_, err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+id, r, nil)
	return err
}

func (c *Client) DeleteRecord(ctx context.Context, zoneID, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+id, nil, nil)
	return err
}

// Tunnel identifies a remotely-managed tunnel.
type Tunnel struct {
	AccountID string
	ID        string
}

// Target is the hostname DNS records point at to reach the tunnel.
func (t Tunnel) Target() string { return t.ID + ".cfargotunnel.com" }

// ParseTunnelToken reads the account and tunnel IDs from a cloudflared token
// (base64 JSON: {"a": account, "t": tunnel, "s": secret}).
func ParseTunnelToken(token string) (Tunnel, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	}
	if err != nil {
		return Tunnel{}, errors.New("tunnel token is not valid base64")
	}
	var t struct {
		A string `json:"a"`
		T string `json:"t"`
	}
	if json.Unmarshal(b, &t) != nil || t.A == "" || t.T == "" {
		return Tunnel{}, errors.New("tunnel token has no account or tunnel ID")
	}
	return Tunnel{AccountID: t.A, ID: t.T}, nil
}

// Ingress is one tunnel route. Unknown fields are kept when rewriting.
type Ingress map[string]any

func (i Ingress) Hostname() string { s, _ := i["hostname"].(string); return s }
func (i Ingress) Service() string  { s, _ := i["service"].(string); return s }

// TunnelIngress returns the tunnel's routes and the rest of its configuration.
func (c *Client) TunnelIngress(ctx context.Context, t Tunnel) ([]Ingress, map[string]any, error) {
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
	var rules []Ingress
	raw, _ := cfg["ingress"].([]any)
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			rules = append(rules, m)
		}
	}
	return rules, cfg, nil
}

// SetTunnelIngress replaces the tunnel's routes, keeping the rest of cfg.
func (c *Client) SetTunnelIngress(ctx context.Context, t Tunnel, cfg map[string]any, rules []Ingress) error {
	cfg["ingress"] = rules
	_, err := c.do(ctx, http.MethodPut, "/accounts/"+t.AccountID+"/cfd_tunnel/"+t.ID+"/configurations", map[string]any{"config": cfg}, nil)
	return err
}

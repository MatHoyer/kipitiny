// Package cloudflare holds what the manager knows of Cloudflare: zones, DNS
// records and remotely-managed tunnels. The API client is in
// infrastructure/cloudflare.
package cloudflare

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ManagedComment marks the DNS records the manager owns; it never touches
// records without it.
const ManagedComment = "managed by kipitiny"

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

type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
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

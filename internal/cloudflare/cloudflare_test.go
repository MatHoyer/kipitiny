package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("tok")
	c.base = srv.URL
	return c
}

func TestZonesPaginates(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		fmt.Fprintf(w, `{"success":true,"result":[{"id":"z%s","name":"zone%s.com"}],"result_info":{"page":%s,"total_pages":2}}`, page, page, page)
	})
	zones, err := c.Zones(context.Background())
	if err != nil || len(zones) != 2 || zones[1].ID != "z2" {
		t.Fatalf("zones = %+v, %v", zones, err)
	}
}

func TestAPIError(t *testing.T) {
	c := fake(t, nil)
	c.token = "wrong"
	_, err := c.Zones(context.Background())
	if !Forbidden(err) || err.Error() != "cloudflare: Authentication error (HTTP 401)" {
		t.Errorf("err = %v", err)
	}
}

func TestCreateRecordMarksIt(t *testing.T) {
	var got Record
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/z1/dns_records" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"success":true,"result":{}}`)
	})
	if err := c.CreateRecord(context.Background(), "z1", Record{Type: "A", Name: "shop.example.com", Content: "203.0.113.7", TTL: 1}); err != nil {
		t.Fatal(err)
	}
	if !got.Managed() || got.Content != "203.0.113.7" {
		t.Errorf("record = %+v", got)
	}
}

func TestTunnelIngressKeepsUnknownFields(t *testing.T) {
	var put map[string]map[string]any
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"success":true,"result":{"config":{"warp-routing":{"enabled":true},"ingress":[{"hostname":"a.example.com","service":"http://x:80","originRequest":{"noTLSVerify":true}},{"service":"http_status:404"}]}}}`)
			return
		}
		json.NewDecoder(r.Body).Decode(&put)
		fmt.Fprint(w, `{"success":true,"result":{}}`)
	})
	tun := Tunnel{AccountID: "acc", ID: "tid"}
	rules, cfg, err := c.TunnelIngress(context.Background(), tun)
	if err != nil || len(rules) != 2 || rules[0].Hostname() != "a.example.com" || rules[1].Service() != "http_status:404" {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	if err := c.SetTunnelIngress(context.Background(), tun, cfg, rules[1:]); err != nil {
		t.Fatal(err)
	}
	if put["config"]["warp-routing"] == nil || len(put["config"]["ingress"].([]any)) != 1 {
		t.Errorf("put = %+v", put)
	}
}

func TestParseTunnelToken(t *testing.T) {
	tok := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"6ff42ae2-765d-4adf-8112-31c55c1551ef","s":"secret"}`))
	tun, err := ParseTunnelToken(tok)
	if err != nil || tun.AccountID != "acc" || tun.Target() != "6ff42ae2-765d-4adf-8112-31c55c1551ef.cfargotunnel.com" {
		t.Errorf("tunnel = %+v, %v", tun, err)
	}
	if _, err := ParseTunnelToken("fake"); err == nil {
		t.Error("expected an error for a bad token")
	}
}

func TestZoneFor(t *testing.T) {
	zones := []Zone{{ID: "1", Name: "example.com"}, {ID: "2", Name: "eu.example.com"}, {ID: "3", Name: "other.org"}}
	for name, want := range map[string]string{
		"example.com":         "1",
		"shop.example.com":    "1",
		"shop.eu.example.com": "2",
		"notexample.com":      "",
	} {
		z, _ := ZoneFor(zones, name)
		if z.ID != want {
			t.Errorf("ZoneFor(%q) = %q, want %q", name, z.ID, want)
		}
	}
}

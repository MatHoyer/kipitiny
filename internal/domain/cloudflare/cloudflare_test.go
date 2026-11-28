package cloudflare

import (
	"encoding/base64"
	"testing"
)

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

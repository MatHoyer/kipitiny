package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// token creates an API token through the session-authenticated client.
func (c *client) token(name string) string {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/tokens", strings.NewReader(`{"name":"t-`+name+`"}`))
	res, err := c.hc.Do(req)
	if err != nil || res.StatusCode != 201 {
		c.t.Fatalf("create token: %v %v", res.StatusCode, err)
	}
	defer res.Body.Close()
	var body struct{ Token string }
	json.NewDecoder(res.Body).Decode(&body)
	return body.Token
}

func TestTokens(t *testing.T) {
	srv := newServer(t)
	admin := newClient(t, srv)
	expect(t, "setup", admin.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	full := admin.token("admin")
	anon := newClient(t, srv)
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	expect(t, "bad token", anon.do("GET", "/api/projects", "", bearer("kpt_nope")...), 401)
	// Creating a project would create a real Docker network: a route on a
	// missing service shows the token is let through without one.
	expect(t, "admin token may change settings (service missing)", anon.do("PATCH", "/api/services/X", `{}`, bearer(full)...), 404)
	expect(t, "tokens can't mint tokens", anon.do("POST", "/api/tokens", `{"name":"x"}`, bearer(full)...), 403)
	expect(t, "tokens can't list tokens", anon.do("GET", "/api/tokens", "", bearer(full)...), 403)

	req, _ := http.NewRequest("GET", srv.URL+"/api/audit", nil)
	res, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var entries []struct{ Actor, Action, Target string }
	json.NewDecoder(res.Body).Decode(&entries)
	found := false
	for _, e := range entries {
		if e.Actor == "token:t-admin" && e.Action == "PATCH /api/services/{id}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("token mutation not audited: %+v", entries)
	}
}

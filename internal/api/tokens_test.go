package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// token creates an API token through the session-authenticated client.
func (c *client) token(scope string) string {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/tokens", strings.NewReader(`{"name":"t-`+scope+`","scope":"`+scope+`"}`))
	res, err := c.hc.Do(req)
	if err != nil || res.StatusCode != 201 {
		c.t.Fatalf("create token: %v %v", res.StatusCode, err)
	}
	defer res.Body.Close()
	var body struct{ Token string }
	json.NewDecoder(res.Body).Decode(&body)
	return body.Token
}

func TestTokenScopes(t *testing.T) {
	srv := newServer(t)
	admin := newClient(t, srv)
	expect(t, "setup", admin.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	read, deploy, full := admin.token("read"), admin.token("deploy"), admin.token("admin")
	anon := newClient(t, srv)
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	expect(t, "bad token", anon.do("GET", "/api/projects", "", bearer("kpt_nope")...), 401)
	expect(t, "read token lists", anon.do("GET", "/api/projects", "", bearer(read)...), 200)
	expect(t, "read token can't create", anon.do("POST", "/api/projects", `{"name":"x"}`, bearer(read)...), 403)
	expect(t, "read token can't reveal secrets", anon.do("GET", "/api/services/X/connection", "", bearer(read)...), 403)
	expect(t, "deploy token may deploy (service missing)", anon.do("POST", "/api/services/X/deploy", "", bearer(deploy)...), 404)
	expect(t, "deploy token can't change settings", anon.do("PATCH", "/api/services/X", `{}`, bearer(deploy)...), 403)
	expect(t, "admin token creates", anon.do("POST", "/api/projects", `{"name":"shop"}`, bearer(full)...), 201)
	expect(t, "tokens can't mint tokens", anon.do("POST", "/api/tokens", `{"name":"x","scope":"admin"}`, bearer(full)...), 403)
	expect(t, "tokens can't list tokens", anon.do("GET", "/api/tokens", "", bearer(full)...), 403)

	req, _ := http.NewRequest("GET", srv.URL+"/api/audit", nil)
	res, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var entries []struct{ Actor, Action string }
	json.NewDecoder(res.Body).Decode(&entries)
	found := false
	for _, e := range entries {
		if e.Actor == "token:t-admin" && e.Action == "POST /api/projects" {
			found = true
		}
	}
	if !found {
		t.Fatalf("token mutation not audited: %+v", entries)
	}
}

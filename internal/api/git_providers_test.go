package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The UI's CSP refuses form posts to GitHub: the launch page sets its own
// (replacing SecureHeaders'), allowing only the forge as a form target.
func TestLaunchGitHubApp(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	expect(t, "setup",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)

	req, _ := http.NewRequest("POST", srv.URL+"/api/git-providers/github", strings.NewReader(`{"name":"GitHub"}`))
	res, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	expect(t, "start", res.StatusCode, 200)
	var start struct{ URL string }
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.URL)

	res, err = c.hc.Get(srv.URL + u.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	expect(t, "launch", res.StatusCode, 200)
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action https://github.com;") || strings.Contains(csp, "'self'") {
		t.Errorf("csp = %s", csp)
	}
	if !strings.Contains(string(page), `action="https://github.com/settings/apps/new?state=`) ||
		!strings.Contains(string(page), `name="manifest" value="{&#34;`) {
		t.Errorf("page = %s", page)
	}

	res, err = c.hc.Get(srv.URL + "/api/git-providers/github/launch?state=forged")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/settings/git-providers" || !strings.Contains(res.Request.URL.RawQuery, "error=") {
		t.Errorf("unknown state landed on %s", res.Request.URL)
	}
}

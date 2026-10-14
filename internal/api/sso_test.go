package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSSOCallbackRedirects(t *testing.T) {
	srv := newServer(t)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, q := range []string{"state=unknown&code=c", "error=access_denied&error_description=nope&state=unknown"} {
		resp, err := hc.Get(srv.URL + "/api/auth/sso/callback?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusSeeOther || loc.Path != "/" || loc.Query().Get("sso-error") == "" {
			t.Errorf("%s: %d %s", q, resp.StatusCode, loc)
		}
		if c := resp.Header.Get("Set-Cookie"); !strings.HasPrefix(c, ssoCookie+"=;") {
			t.Errorf("%s: cookie not cleared: %q", q, c)
		}
	}
	if !strings.Contains(mustGet(t, hc, srv.URL+"/api/auth/sso/callback?error=access_denied&error_description=nope"), "nope") {
		t.Error("provider's error not shown")
	}
}

func mustGet(t *testing.T, hc *http.Client, u string) string {
	t.Helper()
	resp, err := hc.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

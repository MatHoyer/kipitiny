package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// TestRouteAccessOverHTTP sends every route of routeAccess through the real
// handler with API tokens, so a route registered without its check is caught
// where TestMemberRouteAccess, calling authorize directly, would not see it:
// refused without its permission (a member's grants, or a restricted token
// of a member holding everything), not found on another project, admin
// routes refused to a member, and session-only routes refused to any token.
func TestRouteAccessOverHTTP(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock") // nothing reaches a daemon
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	param := regexp.MustCompile(`\{[a-zA-Z]+(\.\.\.)?\}`)
	path := func(pattern string, kind identity.IDKind, ids map[string]string) (string, string) {
		method, p, _ := strings.Cut(pattern, " ")
		return method, strings.ReplaceAll(param.ReplaceAllStringFunc(p, func(s string) string {
			if s == "{id}" && kind != "" {
				return ids[string(kind)]
			}
			return "x"
		}), "//", "/")
	}
	do := func(tok, pattern string, kind identity.IDKind, ids map[string]string) (int, string) {
		t.Helper()
		method, p := path(pattern, kind, ids)
		req, _ := http.NewRequest(method, srv.URL+p, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	ids := func(byKind map[identity.IDKind]string) map[string]string {
		out := map[string]string{}
		for k, v := range byKind {
			out[string(k)] = v
		}
		return out
	}
	shop, other := ids(m.shop), ids(m.other)
	restricted := func(perms []identity.Perm) string {
		t.Helper()
		ps := make([]string, len(perms))
		for i, p := range perms {
			ps[i] = string(p)
		}
		tok, err := m.c.CreateAPIToken(m.as(t, identity.Perms, identity.Features...), identity.TokenInput{Name: "t", Restricted: true,
			Features: []string{}, Grants: map[string][]string{m.shop[identity.IDProject]: ps}})
		if err != nil {
			t.Fatal(err)
		}
		return tok.Token
	}

	for pattern, acc := range routeAccess {
		t.Run(pattern, func(t *testing.T) {
			if sessionOnly(pattern) {
				if code, body := do(m.token(t, identity.Perms...), pattern, acc.kind, shop); code != http.StatusForbidden || !strings.Contains(body, "API tokens") {
					t.Errorf("token on a session-only route: %d %s", code, body)
				}
				return
			}
			switch {
			case acc.anyone:
			case acc.feature != "":
				if code, body := do(m.token(t, identity.Perms...), pattern, acc.kind, shop); code != http.StatusForbidden {
					t.Errorf("member without %s: %d %s", acc.feature, code, body)
				}
			case acc.perm != "":
				if acc.perm != identity.PermView {
					without := slices.DeleteFunc(slices.Clone(identity.Perms), func(p identity.Perm) bool { return p == acc.perm })
					want := "needs the " + string(acc.perm) + " permission"
					if code, body := do(m.token(t, without...), pattern, acc.kind, shop); code != http.StatusForbidden || !strings.Contains(body, want) {
						t.Errorf("member without %s: %d %s", acc.perm, code, body)
					}
					if code, body := do(restricted(without), pattern, acc.kind, shop); code != http.StatusForbidden || !strings.Contains(body, want) {
						t.Errorf("restricted token without %s: %d %s", acc.perm, code, body)
					}
				}
				if code, body := do(m.token(t, identity.Perms...), pattern, acc.kind, other); code != http.StatusNotFound {
					t.Errorf("another project's: %d %s", code, body)
				}
			default:
				if code, body := do(m.token(t, identity.Perms...), pattern, acc.kind, shop); code != http.StatusForbidden || !strings.Contains(body, "admins only") {
					t.Errorf("member: %d %s", code, body)
				}
			}
		})
	}
}

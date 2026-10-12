package api

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// TestRouteAccess makes every route say what a member needs for it, so a
// new one can't silently fall back to admin only (or be forgotten when it
// should be).
func TestRouteAccess(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, m := range regexp.MustCompile(`\bhandle\("([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		routes = append(routes, m[1])
	}
	if len(routes) < 100 {
		t.Fatalf("found only %d routes in api.go", len(routes))
	}
	for _, r := range routes {
		_, listed := routeAccess[r]
		if !listed && !publicRoutes[r] && !dataRoute(r) {
			t.Errorf("%s is missing from routeAccess", r)
		}
		if listed && dataRoute(r) {
			t.Errorf("%s is a data route: core checks it, routeAccess must not list it", r)
		}
	}
	for r := range routeAccess {
		if !slices.Contains(routes, r) {
			t.Errorf("routeAccess lists %s, which isn't a route", r)
		}
	}
}

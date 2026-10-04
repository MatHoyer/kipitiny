package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/cloudflare"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/secrets"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestMergeMiddlewares(t *testing.T) {
	in := MiddlewaresInput{
		BasicAuth: []BasicAuthInput{
			{Name: " ann ", Password: "correct horse"},
			{Name: "bob", Password: "{{ fake://bob }}"},
		},
		IPAllowList: []string{" 10.0.0.0/8 ", ""},
		RateLimit:   &store.RateLimit{Average: 5},
		Headers:     map[string]string{" X-Robots-Tag ": " noindex "},
	}
	m, err := mergeMiddlewares(in, store.Middlewares{})
	if err != nil {
		t.Fatal(err)
	}
	ann, bob := m.BasicAuth[0], m.BasicAuth[1]
	if ann.Name != "ann" || bcrypt.CompareHashAndPassword([]byte(ann.Hash), []byte("correct horse")) != nil || ann.Ref != "" {
		t.Errorf("ann = %+v, want a hash of her password", ann)
	}
	if bob.Ref != "{{ fake://bob }}" || bob.Hash != "" {
		t.Errorf("bob = %+v, want the reference kept unhashed", bob)
	}
	if !slices.Equal(m.IPAllowList, []string{"10.0.0.0/8"}) || m.RateLimit.Burst != 5 || m.Headers["X-Robots-Tag"] != "noindex" {
		t.Errorf("middlewares = %+v", m)
	}

	// An empty password keeps the user's current one.
	kept, err := mergeMiddlewares(MiddlewaresInput{BasicAuth: []BasicAuthInput{{Name: "ann"}, {Name: "bob"}}}, m)
	if err != nil || !slices.Equal(kept.BasicAuth, m.BasicAuth) {
		t.Errorf("kept = %+v, %v", kept.BasicAuth, err)
	}
	for _, u := range []BasicAuthInput{{Name: "new"}, {Name: "x", Password: "short"}, {Name: "x", Password: "{{ project.PW }}"}} {
		if _, err := mergeMiddlewares(MiddlewaresInput{BasicAuth: []BasicAuthInput{u}}, m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", u, err)
		}
	}
}

func TestValidateMiddlewares(t *testing.T) {
	ok := store.Middlewares{
		BasicAuth:   []store.BasicAuthUser{{Name: "ann@example.com", Hash: "h"}},
		IPAllowList: []string{"203.0.113.7", "2001:db8::/32"},
		RateLimit:   &store.RateLimit{Average: 1, Burst: 1},
		Headers:     map[string]string{"X-Robots-Tag": "noindex", "Server": ""},
	}
	if err := validateMiddlewares(ok); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]store.Middlewares{
		"user with colon": {BasicAuth: []store.BasicAuthUser{{Name: "a:b"}}},
		"duplicate user":  {BasicAuth: []store.BasicAuthUser{{Name: "a"}, {Name: "a"}}},
		"bad range":       {IPAllowList: []string{"10.0.0.0/33"}},
		"zero rate":       {RateLimit: &store.RateLimit{Average: 0, Burst: 1}},
		"dotted header":   {Headers: map[string]string{"X.Y": "1"}},
		"multiline value": {Headers: map[string]string{"X-Y": "a\r\nSet-Cookie: x"}},
	} {
		if err := validateMiddlewares(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestMiddlewareLabels(t *testing.T) {
	svc := store.Service{ID: "01ABC", Domain: "shop.example.com", Port: 80, Middlewares: store.Middlewares{
		BasicAuth:   []store.BasicAuthUser{{Name: "ann", Hash: "$2a$04$h"}, {Name: "bob", Ref: "{{ fake://bob }}"}},
		IPAllowList: []string{"10.0.0.0/8", "203.0.113.7"},
		RateLimit:   &store.RateLimit{Average: 10, Burst: 20},
		Headers:     map[string]string{"X-Robots-Tag": "noindex"},
	}}
	l := traefikLabels(svc, route{})
	r := routerName(t, l)
	mw := func(suffix, key string) string { return l["traefik.http.middlewares."+r+"-"+suffix+"."+key] }

	want := []string{r + "-headers@docker", r + "-allow@docker", r + "-ratelimit@docker", r + "-auth@docker", "kipitiny-01abc-retry@docker"}
	if got := strings.Split(l["traefik.http.routers."+r+".middlewares"], ","); !slices.Equal(got, want) {
		t.Errorf("chain = %v, want %v", got, want)
	}
	if mw("headers", "headers.customresponseheaders.X-Robots-Tag") != "noindex" {
		t.Error("response header missing")
	}
	if mw("allow", "ipallowlist.sourcerange") != "10.0.0.0/8,203.0.113.7" || mw("allow", "ipallowlist.ipstrategy.depth") != "" {
		t.Errorf("allowlist = %q, depth %q", mw("allow", "ipallowlist.sourcerange"), mw("allow", "ipallowlist.ipstrategy.depth"))
	}
	if mw("ratelimit", "ratelimit.average") != "10" || mw("ratelimit", "ratelimit.burst") != "20" {
		t.Error("rate limit labels wrong")
	}
	if mw("auth", "basicauth.users") != "ann:$2a$04$h" || mw("auth", "basicauth.removeheader") != "true" {
		t.Errorf("users = %q, want only hashed users", mw("auth", "basicauth.users"))
	}

	// Behind Cloudflare the client IP is the last X-Forwarded-For entry.
	l = traefikLabels(svc, route{behindProxy: true})
	r = routerName(t, l)
	if mw("allow", "ipallowlist.ipstrategy.depth") != "1" || mw("ratelimit", "ratelimit.sourcecriterion.ipstrategy.depth") != "1" {
		t.Error("behind the proxy, the client IP must come from X-Forwarded-For")
	}

	// Same configuration, same router; any change, a new one, so old and
	// new replicas never define the same router differently.
	if r2 := routerName(t, traefikLabels(svc, route{behindProxy: true})); r2 != r {
		t.Errorf("router name unstable: %s, %s", r, r2)
	}
	changed := svc
	changed.Middlewares.Headers = map[string]string{"X-Robots-Tag": "all"}
	if r2 := routerName(t, traefikLabels(changed, route{behindProxy: true})); r2 == r {
		t.Error("changed middlewares kept the router name")
	}
}

func TestHashBasicAuth(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	c.secrets = []secrets.Provider{&fakeProvider{}}
	if _, err := c.ConnectSecretProvider(ctx, "fake", "good"); err != nil {
		t.Fatal(err)
	}
	svc := store.Service{Middlewares: store.Middlewares{BasicAuth: []store.BasicAuthUser{
		{Name: "ann", Hash: "kept"},
		{Name: "bob", Ref: "{{ fake://bob }}"},
	}}}
	stored := slices.Clone(svc.Middlewares.BasicAuth)
	n, err := c.hashBasicAuth(ctx, &svc)
	if err != nil || n != 1 {
		t.Fatalf("hashBasicAuth = %d, %v", n, err)
	}
	if svc.Middlewares.BasicAuth[0].Hash != "kept" ||
		bcrypt.CompareHashAndPassword([]byte(svc.Middlewares.BasicAuth[1].Hash), []byte("value-of-bob")) != nil {
		t.Errorf("users = %+v", svc.Middlewares.BasicAuth)
	}
	if stored[1].Hash != "" {
		t.Error("hashing mutated the stored users")
	}

	if err := c.checkBasicAuthSchemes(store.Middlewares{BasicAuth: []store.BasicAuthUser{{Name: "x", Ref: "{{ op://v/i/f }}"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scheme accepted: %v", err)
	}
	if m := masked(svc).Middlewares; m.BasicAuth[0].Hash != "" || svc.Middlewares.BasicAuth[0].Hash != "kept" {
		t.Error("masking must hide hashes without touching the service")
	}
}

func TestTraefikTrustsCloudflare(t *testing.T) {
	c := &Core{}
	trusted := func(o traefikOpts) string {
		opts, _ := c.traefikSpec("/var/run/docker.sock", o)
		for _, a := range opts.Config.Cmd {
			if v, ok := strings.CutPrefix(a, "--entrypoints.websecure.forwardedheaders.trustedips="); ok {
				return v
			}
		}
		return ""
	}
	if got := trusted(traefikOpts{}); got != "" {
		t.Errorf("direct traffic trusts %s", got)
	}
	if got := trusted(traefikOpts{Tunnel: true}); !strings.Contains(got, "172.16.0.0/12") {
		t.Errorf("tunnel trusts %q, want the proxy network", got)
	}
	if got := trusted(traefikOpts{CFToken: "tok"}); !strings.Contains(got, "173.245.48.0/20") {
		t.Errorf("Cloudflare connected trusts %q, want its edge", got)
	}
}

func TestBehindCloudflare(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if c.behindCloudflare(ctx, store.LocalServerID, "shop.example.com") {
		t.Error("not connected")
	}
	s := c.cf(ctx)
	s.token, s.zones = "tok", []cloudflare.Zone{{ID: "z", Name: "example.com"}}
	d, err := c.store.CreateDomain(ctx, store.Domain{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if c.behindCloudflare(ctx, store.LocalServerID, "shop.example.com") {
		t.Error("DNS only record")
	}
	if _, err := c.store.SetDomainProxied(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if !c.behindCloudflare(ctx, store.LocalServerID, "shop.example.com") {
		t.Error("proxied record")
	}
	if c.behindCloudflare(ctx, store.LocalServerID, "shop.other.org") {
		t.Error("zone the token doesn't manage")
	}
	c.cfg.Tunnel.Token = "t"
	if !c.behindCloudflare(ctx, store.LocalServerID, "shop.other.org") {
		t.Error("behind the tunnel")
	}
}

package network

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/kipit-labs/kipitiny/internal/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestMergeMiddlewares(t *testing.T) {
	in := MiddlewaresInput{
		BasicAuth: []BasicAuthInput{
			{Name: " ann ", Password: "correct horse"},
			{Name: "bob", Password: "{{ fake://bob }}"},
		},
		IPAllowList: []string{" 10.0.0.0/8 ", ""},
		RateLimit:   &domain.RateLimit{Average: 5},
		Headers:     map[string]string{" X-Robots-Tag ": " noindex "},
	}
	m, err := MergeMiddlewares(in, domain.Middlewares{})
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
	kept, err := MergeMiddlewares(MiddlewaresInput{BasicAuth: []BasicAuthInput{{Name: "ann"}, {Name: "bob"}}}, m)
	if err != nil || !slices.Equal(kept.BasicAuth, m.BasicAuth) {
		t.Errorf("kept = %+v, %v", kept.BasicAuth, err)
	}
	for _, u := range []BasicAuthInput{{Name: "new"}, {Name: "x", Password: "short"}, {Name: "x", Password: "{{ project.PW }}"}} {
		if _, err := MergeMiddlewares(MiddlewaresInput{BasicAuth: []BasicAuthInput{u}}, m); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v accepted: %v", u, err)
		}
	}
}

func TestValidateMiddlewares(t *testing.T) {
	ok := domain.Middlewares{
		BasicAuth:   []domain.BasicAuthUser{{Name: "ann@example.com", Hash: "h"}},
		IPAllowList: []string{"203.0.113.7", "2001:db8::/32"},
		RateLimit:   &domain.RateLimit{Average: 1, Burst: 1},
		Headers:     map[string]string{"X-Robots-Tag": "noindex", "Server": ""},
	}
	if err := ValidateMiddlewares(ok); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]domain.Middlewares{
		"user with colon": {BasicAuth: []domain.BasicAuthUser{{Name: "a:b"}}},
		"duplicate user":  {BasicAuth: []domain.BasicAuthUser{{Name: "a"}, {Name: "a"}}},
		"bad range":       {IPAllowList: []string{"10.0.0.0/33"}},
		"zero rate":       {RateLimit: &domain.RateLimit{Average: 0, Burst: 1}},
		"dotted header":   {Headers: map[string]string{"X.Y": "1"}},
		"multiline value": {Headers: map[string]string{"X-Y": "a\r\nSet-Cookie: x"}},
	} {
		if err := ValidateMiddlewares(m); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestMiddlewareLabels(t *testing.T) {
	svc := domain.Service{ID: "01ABC", Domain: "shop.example.com", Port: 80, Middlewares: domain.Middlewares{
		BasicAuth:   []domain.BasicAuthUser{{Name: "ann", Hash: "$2a$04$h"}, {Name: "bob", Ref: "{{ fake://bob }}"}},
		IPAllowList: []string{"10.0.0.0/8", "203.0.113.7"},
		RateLimit:   &domain.RateLimit{Average: 10, Burst: 20},
		Headers:     map[string]string{"X-Robots-Tag": "noindex"},
	}}
	l := TraefikLabels(svc, Routing{})
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
	l = TraefikLabels(svc, Routing{BehindProxy: true})
	r = routerName(t, l)
	if mw("allow", "ipallowlist.ipstrategy.depth") != "1" || mw("ratelimit", "ratelimit.sourcecriterion.ipstrategy.depth") != "1" {
		t.Error("behind the proxy, the client IP must come from X-Forwarded-For")
	}

	// Same configuration, same router; any change, a new one, so old and
	// new replicas never define the same router differently.
	if r2 := routerName(t, TraefikLabels(svc, Routing{BehindProxy: true})); r2 != r {
		t.Errorf("router name unstable: %s, %s", r, r2)
	}
	changed := svc
	changed.Middlewares.Headers = map[string]string{"X-Robots-Tag": "all"}
	if r2 := routerName(t, TraefikLabels(changed, Routing{BehindProxy: true})); r2 == r {
		t.Error("changed middlewares kept the router name")
	}
}

func TestTraefikTrustsCloudflare(t *testing.T) {
	c := &Network{cfg: &config.Config{}}
	trusted := func(o TraefikOpts) string {
		opts, _ := c.TraefikSpec("/var/run/docker.sock", o)
		for _, a := range opts.Config.Cmd {
			if v, ok := strings.CutPrefix(a, "--entrypoints.websecure.forwardedheaders.trustedips="); ok {
				return v
			}
		}
		return ""
	}
	if got := trusted(TraefikOpts{}); got != "" {
		t.Errorf("direct traffic trusts %s", got)
	}
	if got := trusted(TraefikOpts{Tunnel: true}); !strings.Contains(got, "172.16.0.0/12") {
		t.Errorf("tunnel trusts %q, want the proxy network", got)
	}
	if got := trusted(TraefikOpts{CFToken: "tok"}); !strings.Contains(got, "173.245.48.0/20") {
		t.Errorf("Cloudflare connected trusts %q, want its edge", got)
	}
}

func TestBehindCloudflare(t *testing.T) {
	ctx := context.Background()
	c := newTestNetwork(t, config.Config{})
	if c.behindCloudflare(ctx, domain.LocalServerID, "shop.example.com") {
		t.Error("not connected")
	}
	s := c.cf(ctx)
	s.token, s.zones = "tok", []cloudflare.Zone{{ID: "z", Name: "example.com"}}
	d, err := c.store.CreateDomain(ctx, domain.Domain{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if c.behindCloudflare(ctx, domain.LocalServerID, "shop.example.com") {
		t.Error("DNS only record")
	}
	if _, err := c.store.SetDomainProxied(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if !c.behindCloudflare(ctx, domain.LocalServerID, "shop.example.com") {
		t.Error("proxied record")
	}
	if c.behindCloudflare(ctx, domain.LocalServerID, "shop.other.org") {
		t.Error("zone the token doesn't manage")
	}
	c.cfg.Tunnel.Token = "t"
	if !c.behindCloudflare(ctx, domain.LocalServerID, "shop.other.org") {
		t.Error("behind the tunnel")
	}
}

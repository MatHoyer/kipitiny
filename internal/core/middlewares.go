package core

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/secrets"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxBasicAuthUsers = 20
	maxAllowedRanges  = 50
	maxHeaders        = 20
	// Traefik checks the hash on every request, so a high cost would slow
	// every page load; the hashes only live in the manager and on labels.
	basicAuthCost = bcrypt.MinCost
)

var (
	basicAuthUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	headerNameRe    = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// MiddlewaresInput sets an app's Traefik middlewares; see store.Middlewares.
type MiddlewaresInput struct {
	BasicAuth   []BasicAuthInput  `json:"basicAuth"`
	IPAllowList []string          `json:"ipAllowList"`
	RateLimit   *store.RateLimit  `json:"rateLimit"`
	Headers     map[string]string `json:"headers"`
}

// BasicAuthInput is a basic auth user as typed. Password is the password, a
// password manager reference ({{ scheme://… }}), or empty to keep the
// user's current one.
type BasicAuthInput struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	// Hash is an existing bcrypt hash, from a compose file; not settable
	// through the API, which takes passwords.
	Hash string `json:"-"`
}

// mergeMiddlewares builds the stored middlewares from in, hashing new
// passwords and keeping the ones left empty from old.
func mergeMiddlewares(in MiddlewaresInput, old store.Middlewares) (store.Middlewares, error) {
	m := store.Middlewares{RateLimit: in.RateLimit}
	if m.RateLimit != nil && m.RateLimit.Burst == 0 {
		m.RateLimit.Burst = m.RateLimit.Average
	}
	for _, r := range in.IPAllowList {
		if r = strings.TrimSpace(r); r != "" {
			m.IPAllowList = append(m.IPAllowList, r)
		}
	}
	if len(in.Headers) > 0 {
		m.Headers = make(map[string]string, len(in.Headers))
		for k, v := range in.Headers {
			m.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	for _, u := range in.BasicAuth {
		user := store.BasicAuthUser{Name: strings.TrimSpace(u.Name)}
		pw := strings.TrimSpace(u.Password)
		switch {
		case u.Hash != "":
			if _, err := bcrypt.Cost([]byte(u.Hash)); err != nil {
				return store.Middlewares{}, fmt.Errorf("%w: basic auth user %q: not a bcrypt hash", ErrInvalid, user.Name)
			}
			user.Hash = u.Hash
		case pw == "":
			i := slices.IndexFunc(old.BasicAuth, func(o store.BasicAuthUser) bool { return o.Name == user.Name })
			if i < 0 {
				return store.Middlewares{}, fmt.Errorf("%w: basic auth user %q needs a password", ErrInvalid, user.Name)
			}
			user.Hash, user.Ref = old.BasicAuth[i].Hash, old.BasicAuth[i].Ref
		case soleRefRe.MatchString(pw):
			if soleRefRe.FindStringSubmatch(pw)[4] == "" {
				return store.Middlewares{}, fmt.Errorf("%w: basic auth user %q: only password manager references are allowed", ErrInvalid, user.Name)
			}
			user.Ref = pw
		default:
			if len(pw) < minPasswordLength || len(pw) > maxPasswordBytes {
				return store.Middlewares{}, fmt.Errorf("%w: basic auth passwords must be %d to %d characters", ErrInvalid, minPasswordLength, maxPasswordBytes)
			}
			h, err := bcrypt.GenerateFromPassword([]byte(pw), basicAuthCost)
			if err != nil {
				return store.Middlewares{}, err
			}
			user.Hash = string(h)
		}
		m.BasicAuth = append(m.BasicAuth, user)
	}
	return m, nil
}

// validateMiddlewares checks what Traefik labels can't carry or would reject.
func validateMiddlewares(m store.Middlewares) error {
	if len(m.BasicAuth) > maxBasicAuthUsers {
		return fmt.Errorf("%w: at most %d basic auth users", ErrInvalid, maxBasicAuthUsers)
	}
	names := map[string]bool{}
	for _, u := range m.BasicAuth {
		if !basicAuthUserRe.MatchString(u.Name) {
			return fmt.Errorf("%w: basic auth user %q must be letters, digits and . _ @ - (max 64)", ErrInvalid, u.Name)
		}
		if names[u.Name] {
			return fmt.Errorf("%w: duplicate basic auth user %q", ErrInvalid, u.Name)
		}
		names[u.Name] = true
	}
	if len(m.IPAllowList) > maxAllowedRanges {
		return fmt.Errorf("%w: at most %d allowed IP ranges", ErrInvalid, maxAllowedRanges)
	}
	for _, r := range m.IPAllowList {
		if _, err := netip.ParsePrefix(r); err != nil {
			if _, err := netip.ParseAddr(r); err != nil {
				return fmt.Errorf("%w: %q is not an IP address or CIDR range", ErrInvalid, r)
			}
		}
	}
	if rl := m.RateLimit; rl != nil && (rl.Average < 1 || rl.Burst < 1) {
		return fmt.Errorf("%w: rate limit and burst must be at least 1", ErrInvalid)
	}
	if len(m.Headers) > maxHeaders {
		return fmt.Errorf("%w: at most %d response headers", ErrInvalid, maxHeaders)
	}
	for k, v := range m.Headers {
		if !headerNameRe.MatchString(k) {
			return fmt.Errorf("%w: header name %q must be letters, digits and dashes", ErrInvalid, k)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%w: header %s: value must be one line", ErrInvalid, k)
		}
	}
	return nil
}

// checkBasicAuthSchemes refuses references no password manager handles.
func (c *Core) checkBasicAuthSchemes(m store.Middlewares) error {
	for _, u := range m.BasicAuth {
		if ref := basicAuthRef(u); ref != "" && c.providerFor(secrets.SchemeOf(ref)) == nil {
			return fmt.Errorf("%w: basic auth user %s: no password manager handles %s", ErrInvalid, u.Name, ref)
		}
	}
	return nil
}

// basicAuthRef is the password manager reference inside u.Ref, if any.
func basicAuthRef(u store.BasicAuthUser) string {
	if m := soleRefRe.FindStringSubmatch(u.Ref); m != nil {
		return m[4]
	}
	return ""
}

// hashBasicAuth fetches the passwords of svc's reference users and hashes
// them into svc, for the deployment snapshot replicas are created from.
// It reports how many it fetched.
func (c *Core) hashBasicAuth(ctx context.Context, svc *store.Service) (int, error) {
	var refs []string
	for _, u := range svc.Middlewares.BasicAuth {
		if ref := basicAuthRef(u); ref != "" {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return 0, nil
	}
	vals, err := c.resolveSecrets(ctx, refs)
	if err != nil {
		return 0, err
	}
	users := slices.Clone(svc.Middlewares.BasicAuth)
	for i, u := range users {
		ref := basicAuthRef(u)
		if ref == "" {
			continue
		}
		pw := vals[ref]
		if pw == "" || len(pw) > maxPasswordBytes {
			return 0, fmt.Errorf("basic auth user %s: %s must hold a password of at most %d bytes", u.Name, ref, maxPasswordBytes)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), basicAuthCost)
		if err != nil {
			return 0, err
		}
		users[i].Hash = string(h)
	}
	svc.Middlewares.BasicAuth = users
	return len(refs), nil
}

// maskMiddlewares hides password hashes.
func maskMiddlewares(m store.Middlewares) store.Middlewares {
	m.BasicAuth = slices.Clone(m.BasicAuth)
	for i := range m.BasicAuth {
		m.BasicAuth[i].Hash = ""
	}
	return m
}

// middlewareLabels is the Traefik configuration of each configured
// middleware, by middleware suffix then label key (below
// traefik.http.middlewares.<name>), in the order requests go through them.
// behindProxy takes the client IP from X-Forwarded-For, where Cloudflare
// puts it.
func middlewareLabels(m store.Middlewares, behindProxy bool) []middleware {
	var out []middleware
	// First, so that refused requests get the headers too.
	if len(m.Headers) > 0 {
		l := map[string]string{}
		for k, v := range m.Headers {
			l["headers.customresponseheaders."+k] = v
		}
		out = append(out, middleware{"headers", l})
	}
	if len(m.IPAllowList) > 0 {
		l := map[string]string{"ipallowlist.sourcerange": strings.Join(m.IPAllowList, ",")}
		if behindProxy {
			l["ipallowlist.ipstrategy.depth"] = "1"
		}
		out = append(out, middleware{"allow", l})
	}
	if rl := m.RateLimit; rl != nil {
		l := map[string]string{
			"ratelimit.average": strconv.Itoa(rl.Average),
			"ratelimit.burst":   strconv.Itoa(rl.Burst),
		}
		if behindProxy {
			l["ratelimit.sourcecriterion.ipstrategy.depth"] = "1"
		}
		out = append(out, middleware{"ratelimit", l})
	}
	if len(m.BasicAuth) > 0 {
		users := make([]string, 0, len(m.BasicAuth))
		for _, u := range m.BasicAuth {
			// A user without a hash (a reference never fetched) can't log
			// in; the others still can, and nobody gets in without one.
			if u.Hash != "" {
				users = append(users, u.Name+":"+u.Hash)
			}
		}
		out = append(out, middleware{"auth", map[string]string{
			"basicauth.users": strings.Join(users, ","),
			// The app has no use for the shared credentials.
			"basicauth.removeheader": "true",
		}})
	}
	return out
}

type middleware struct {
	Suffix string
	Labels map[string]string
}

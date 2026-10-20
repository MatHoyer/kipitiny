package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
)

const sessionCookie = "kipitiny_session"

// publicRoutes are reachable without a session.
var publicRoutes = map[string]bool{
	"GET /api/health":     true,
	"GET /api/auth/state": true,
	// The logo shows on the sign-in page and as the favicon.
	"GET /api/branding/logo":         true,
	"GET /api/manifest.webmanifest":  true,
	"POST /api/auth/setup":           true,
	"POST /api/auth/login":           true,
	"POST /api/auth/login/mfa":       true,
	"POST /api/auth/passkey/begin":   true,
	"POST /api/auth/passkey/finish":  true,
	"POST /api/auth/sso/{id}/begin":  true,
	"POST /api/auth/sso/{id}/invite": true,
	// The flow's state and the browser's cookie authenticate it.
	"GET /api/auth/sso/callback": true,
	// An invite link's token is its credential.
	"POST /api/auth/invite":        true,
	"POST /api/auth/invite/accept": true,
	// Push webhooks authenticate with the project's webhook secret.
	"POST /api/hooks/projects/{id}": true,
	// A GitHub App's webhooks, signed with its secret.
	"POST /api/hooks/git-providers/{id}": true,
}

// sessionOnly routes can't be used with an API token (a token must not mint
// tokens or act as a signed-in user).
func sessionOnly(pattern string) bool {
	return strings.Contains(pattern, "/api/auth/") || strings.Contains(pattern, "/api/account") ||
		strings.Contains(pattern, "/api/tokens") ||
		pattern == "POST /api/projects/{id}/export" || // every secret of a project
		pattern == "POST /api/update" ||
		// The forge sends the browser back to these after a connection.
		strings.HasPrefix(pattern, "GET /api/git-providers/github/") || pattern == "GET /api/git-providers/oauth/callback"
}

// protect enforces same-origin mutations, authentication (session cookie or
// bearer token), and records every mutation in the audit log.
func (a *API) protect(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		_, pattern := mux.Handler(r)
		if publicRoutes[pattern] {
			mux.ServeHTTP(w, r)
			return
		}
		actor, isToken, err := a.authenticate(r)
		if err != nil {
			a.fail(w, err)
			return
		}
		if isToken && sessionOnly(pattern) {
			writeError(w, http.StatusForbidden, "not available to API tokens")
			return
		}
		ctx := core.WithActor(r.Context(), actor)
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			mux.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		req := r.WithContext(ctx)
		mux.ServeHTTP(rec, req) // the mux sets path values on req
		if !strings.HasPrefix(pattern, "POST /api/auth/") {
			a.core.Audit(ctx, pattern, req.PathValue("id"), rec.status, nil)
		}
	})
}

// Authenticated protects a non-API handler (e.g. /mcp) with the same session
// or token authentication; the handler enforces permissions itself.
func (a *API) Authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		actor, _, err := a.authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kipitiny"`)
			a.fail(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(core.WithActor(r.Context(), actor)))
	})
}

// authenticate resolves the caller from a bearer token or the session cookie.
func (a *API) authenticate(r *http.Request) (core.Actor, bool, error) {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		actor, err := a.core.AuthenticateToken(r.Context(), strings.TrimSpace(token))
		return actor, true, err
	}
	actor, err := a.core.SessionActor(r.Context(), sessionToken(r))
	return actor, false, err
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush on the real writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// sameOrigin rejects state-changing requests whose Origin doesn't match the
// host. SameSite=Lax cookies already block most CSRF; this closes the rest.
func sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *API) authState(w http.ResponseWriter, r *http.Request) {
	st, err := a.core.AuthState(r.Context(), sessionToken(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type credentials struct {
	SetupToken string `json:"setupToken,omitempty"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow(r.Context(), a.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.core.Setup(r.Context(), c.SetupToken, c.Username, c.Password)
	if err != nil {
		a.limiter.fail(r.Context(), a.clientIP(r))
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.limiter.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	res, err := a.core.Login(r.Context(), c.Username, c.Password)
	if err != nil {
		a.limiter.fail(r.Context(), ip)
		a.fail(w, err)
		return
	}
	if res.MFATicket != "" {
		// The limiter isn't reset until the second factor passes too, so
		// codes can't be guessed by signing in again and again.
		writeJSON(w, http.StatusOK, map[string]any{"mfaRequired": true, "ticket": res.MFATicket})
		return
	}
	a.limiter.reset(ip)
	setSessionCookie(w, r, res.Session, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, res.User)
}

func (a *API) loginMFA(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.limiter.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, token, err := a.core.LoginMFA(r.Context(), body.Ticket, body.Code)
	if err != nil {
		a.limiter.fail(r.Context(), ip)
		a.fail(w, err)
		return
	}
	a.limiter.reset(ip)
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, u)
}

func (a *API) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.begins.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	a.begins.fail(r.Context(), ip) // every start counts: each one holds server memory
	opts, err := a.core.BeginPasskeyLogin(r.Context(), relyingParty(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (a *API) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.limiter.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var body struct {
		Ceremony   string          `json:"ceremony"`
		Credential json.RawMessage `json:"credential"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, token, err := a.core.FinishPasskeyLogin(r.Context(), body.Ceremony, body.Credential)
	if err != nil {
		a.limiter.fail(r.Context(), ip)
		a.fail(w, err)
		return
	}
	a.limiter.reset(ip)
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, u)
}

// relyingParty is the origin the browser is on, which passkeys are bound to.
// sameOrigin already checked that Origin matches the host.
func relyingParty(r *http.Request) core.RelyingParty {
	origin := r.Header.Get("Origin")
	if origin == "" {
		scheme := "http"
		if isHTTPS(r) {
			scheme = "https"
		}
		origin = scheme + "://" + r.Host
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return core.RelyingParty{ID: host, Origin: origin}
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.core.Logout(r.Context(), sessionToken(r)); err != nil {
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// clientIP is the address the login limiter counts failures against.
func (a *API) clientIP(r *http.Request) string {
	trusted := func(ip netip.Addr) bool { return a.core.TrustedProxy(r.Context(), ip) }
	return clientIP(r, trusted, a.core.BehindTunnel(r.Context()))
}

// clientIP is the socket address, except from a trusted proxy (Traefik), where
// it is the last X-Forwarded-For hop: the one the proxy added. Trusting the
// header from any other peer would let it bypass the login limiter, including
// app containers on kipitiny-proxy that can reach the manager directly;
// ignoring it behind Traefik would lock everyone out at once. Behind the
// Cloudflare tunnel every request reaches Traefik from cloudflared, so the
// client is Cloudflare's CF-Connecting-IP.
func clientIP(r *http.Request, trusted func(netip.Addr) bool, tunnel bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, err := netip.ParseAddr(host); err != nil || !trusted(ip) {
		return host
	}
	if cf := r.Header.Get("CF-Connecting-IP"); tunnel && net.ParseIP(cf) != nil {
		return cf
	}
	xff := r.Header.Values("X-Forwarded-For")
	if len(xff) == 0 {
		return host
	}
	hops := strings.Split(xff[len(xff)-1], ",")
	if last := strings.TrimSpace(hops[len(hops)-1]); net.ParseIP(last) != nil {
		return last
	}
	return host
}

// maxTrackedIPs caps how many addresses a limiter remembers: entries only
// expire when their IP comes back, so a caller rotating addresses (IPv6)
// would otherwise grow the map forever.
const maxTrackedIPs = 10_000

// failLimiter blocks an IP after too many failed attempts within a window.
// Attempts still running count too: concurrent requests would otherwise all
// pass the check before any of them fails.
type failLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	busy   map[string]map[context.Context]bool // attempts running, by request
}

func newFailLimiter(max int, window time.Duration) *failLimiter {
	return &failLimiter{max: max, window: window, fails: map[string][]time.Time{}, busy: map[string]map[context.Context]bool{}}
}

// allow reports whether ip may make an attempt, which then counts until it
// fails or ctx (the request's) ends.
func (l *failLimiter) allow(ctx context.Context, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recent(ip))+len(l.busy[ip]) >= l.max {
		return false
	}
	if l.busy[ip] == nil {
		l.busy[ip] = map[context.Context]bool{}
	}
	l.busy[ip][ctx] = true
	context.AfterFunc(ctx, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.done(ctx, ip)
	})
	return true
}

// fail records ctx's attempt as failed.
func (l *failLimiter) fail(ctx context.Context, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.done(ctx, ip)
	if _, ok := l.fails[ip]; !ok && len(l.fails) >= maxTrackedIPs {
		l.evict()
	}
	l.fails[ip] = append(l.recent(ip), time.Now())
}

// done stops counting ctx's attempt as running; callers hold mu.
func (l *failLimiter) done(ctx context.Context, ip string) {
	if delete(l.busy[ip], ctx); len(l.busy[ip]) == 0 {
		delete(l.busy, ip)
	}
}

// evict drops every expired entry, then arbitrary ones if the map is still
// full; callers hold mu.
func (l *failLimiter) evict() {
	for ip := range l.fails {
		l.recent(ip)
	}
	for ip := range l.fails {
		if len(l.fails) < maxTrackedIPs {
			break
		}
		delete(l.fails, ip)
	}
}

func (l *failLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

// recent drops expired entries; callers hold mu.
func (l *failLimiter) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	ts := l.fails[ip]
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = ts
	}
	return ts
}

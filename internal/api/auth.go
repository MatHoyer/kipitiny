package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const sessionCookie = "kipitiny_session"

// publicRoutes are reachable without a session.
var publicRoutes = map[string]bool{
	"GET /api/health":      true,
	"GET /api/auth/state":  true,
	"POST /api/auth/setup": true,
	"POST /api/auth/login": true,
}

// sessionOnly routes can't be used with an API token (a token must not mint
// tokens or act as a signed-in user).
func sessionOnly(pattern string) bool {
	return strings.Contains(pattern, "/api/auth/") || strings.Contains(pattern, "/api/tokens") ||
		pattern == "POST /api/update"
}

// deployRoutes are the mutations a deploy-scoped token may perform.
var deployRoutes = map[string]bool{
	"POST /api/services/{id}/deploy":   true,
	"POST /api/services/{id}/rollback": true,
	"POST /api/services/{id}/{action}": true, // start, stop, restart
	"POST /api/services/{id}/backups":  true,
	"POST /api/projects/{id}/backups":  true,
	"POST /api/backups/{id}/verify":    true,
}

// secretReads reveal credentials or backup contents: admin only.
var secretReads = map[string]bool{
	"GET /api/services/{id}/connection": true,
	"GET /api/services/{id}/webhook":    true,
	"GET /api/backup-targets/{id}/key":  true,
	"GET /api/backups/{id}/download":    true,
	"GET /api/audit":                    true,
	// A pending Proton sign-in's link.
	"GET /api/proton-logins/{id}": true,
	// Names only, but they map what the password manager token can read.
	"GET /api/secret-providers/{id}/vaults": true,
	"GET /api/secret-providers/{id}/items":  true,
}

// requiredScope maps a route to the scope an API token needs.
func requiredScope(pattern string) store.Scope {
	switch {
	case secretReads[pattern]:
		return store.ScopeAdmin
	case strings.HasPrefix(pattern, "GET "):
		return store.ScopeRead
	case deployRoutes[pattern]:
		return store.ScopeDeploy
	}
	return store.ScopeAdmin
}

// protect enforces same-origin mutations, authentication (session cookie or
// bearer token), token scopes, and records every mutation in the audit log.
func (a *API) protect(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		_, pattern := mux.Handler(r)
		// Push webhooks authenticate with their own secret.
		if publicRoutes[pattern] || pattern == "POST /api/hooks/{id}" {
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
		if !actor.Scope.Allows(requiredScope(pattern)) {
			writeError(w, http.StatusForbidden, fmt.Sprintf("this token's scope (%s) does not allow that", actor.Scope))
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
// or token authentication; the handler enforces scopes itself.
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
	u, err := a.core.Authenticate(r.Context(), sessionToken(r))
	if err != nil {
		return core.Actor{}, false, err
	}
	return core.Actor{Kind: "user", Name: u.Username, Scope: store.ScopeAdmin}, false, nil
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
	if !a.limiter.allow(clientIP(r, a.core.BehindTunnel(r.Context()))) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.core.Setup(r.Context(), c.SetupToken, c.Username, c.Password)
	if err != nil {
		a.limiter.fail(clientIP(r, a.core.BehindTunnel(r.Context())))
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, a.core.BehindTunnel(r.Context()))
	if !a.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.core.Login(r.Context(), c.Username, c.Password)
	if err != nil {
		a.limiter.fail(ip)
		a.fail(w, err)
		return
	}
	a.limiter.reset(ip)
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, u)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.core.Logout(r.Context(), sessionToken(r)); err != nil {
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// clientIP is the socket address, except behind a proxy on a private network
// (Traefik), where it is the last X-Forwarded-For hop: the one the proxy
// added. Trusting the header from public peers would let anyone bypass the
// login limiter; ignoring it behind Traefik would lock everyone out at once.
// Behind the Cloudflare tunnel every request reaches Traefik from cloudflared,
// so the client is Cloudflare's CF-Connecting-IP (no port is published there
// to send a forged one directly).
func clientIP(r *http.Request, tunnel bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
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

// failLimiter blocks an IP after too many failed attempts within a window.
type failLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newFailLimiter(max int, window time.Duration) *failLimiter {
	return &failLimiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func (l *failLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) < l.max
}

func (l *failLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip), time.Now())
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

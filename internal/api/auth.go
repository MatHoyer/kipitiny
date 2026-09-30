package api

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const sessionCookie = "kipitiny_session"

type ctxKey struct{}

// UserFrom returns the authenticated user of the request.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(store.User)
	return u, ok
}

// publicRoutes are reachable without a session.
var publicRoutes = map[string]bool{
	"GET /api/health":      true,
	"GET /api/auth/state":  true,
	"POST /api/auth/setup": true,
	"POST /api/auth/login": true,
}

// protect enforces same-origin mutations and a valid session on every
// non-public route.
func (a *API) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if publicRoutes[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		u, err := a.core.Authenticate(r.Context(), sessionToken(r))
		if err != nil {
			a.fail(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

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
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
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
	if !a.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.core.Setup(r.Context(), c.SetupToken, c.Username, c.Password)
	if err != nil {
		a.limiter.fail(clientIP(r))
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
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

// clientIP uses the socket address only: trusting X-Forwarded-For would let
// anyone bypass the login limiter.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
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

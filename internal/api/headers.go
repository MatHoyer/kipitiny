package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
)

// csp allows only the manager's own assets. Styles need 'unsafe-inline':
// Radix and sonner inject <style> elements at runtime.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// SecureHeaders sets browser hardening headers on every response, and HSTS
// once the request came over HTTPS (directly or through Traefik). It drops
// X-Forwarded-Proto unless a trusted proxy sent the request, so no handler
// takes a plain HTTP request for HTTPS.
func SecureHeaders(next http.Handler, trusted func(context.Context, netip.Addr) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") != "" && !fromTrusted(r, trusted) {
			r.Header.Del("X-Forwarded-Proto")
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func fromTrusted(r *http.Request, trusted func(context.Context, netip.Addr) bool) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && trusted(r.Context(), ip)
}

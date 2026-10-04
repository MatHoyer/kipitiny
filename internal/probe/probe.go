// Package probe implements `kipitiny probe`, the healthcheck the manager
// injects into app containers (whose images may have no curl or wget), and
// the HTTP request behind uptime checks.
package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const timeout = 3 * time.Second

// Check succeeds when tcp://host:port accepts a connection, or when an
// http:// URL answers with a status below 400.
func Check(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "tcp":
		conn, err := net.DialTimeout("tcp", u.Host, timeout)
		if err != nil {
			return err
		}
		return conn.Close()
	case "http":
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := HTTP(ctx, NewClient(), target, 0)
		return err
	default:
		return fmt.Errorf("unsupported probe %q", target)
	}
}

// NewClient returns a client that doesn't follow redirects: a redirect (e.g.
// to a login page) still proves the app serves.
func NewClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Result is the answer to an HTTP check.
type Result struct {
	Status  int
	Latency time.Duration
}

// HTTP gets target with hc and checks the status: expect, or any below 400
// when expect is 0. The result is set whenever a response arrived, even with
// the wrong status. ctx bounds the whole request.
func HTTP(ctx context.Context, hc *http.Client, target string, expect int) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "kipitiny-probe")
	start := time.Now()
	res, err := hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	// Some servers only count a request once its body is read.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	r := Result{Status: res.StatusCode, Latency: time.Since(start)}
	switch {
	case expect != 0 && res.StatusCode != expect:
		return r, fmt.Errorf("status %d, expected %d", res.StatusCode, expect)
	case expect == 0 && res.StatusCode >= 400:
		return r, fmt.Errorf("status %d", res.StatusCode)
	}
	return r, nil
}

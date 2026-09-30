// Package probe implements `kipitiny probe`, the healthcheck the manager
// injects into app containers (whose images may have no curl or wget).
package probe

import (
	"fmt"
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
		client := &http.Client{
			Timeout: timeout,
			// A redirect (e.g. to a login page) still proves the app serves.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		res, err := client.Get(target)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode >= 400 {
			return fmt.Errorf("status %d", res.StatusCode)
		}
		return nil
	default:
		return fmt.Errorf("unsupported probe %q", target)
	}
}

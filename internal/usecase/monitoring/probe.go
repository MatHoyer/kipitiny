package monitoring

// The HTTP request behind uptime checks, and `kipitiny probe`'s for http://.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewProbeClient returns a client that doesn't follow redirects: a redirect (e.g.
// to a login page) still proves the app serves.
func NewProbeClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ProbeResult is the answer to an HTTP check.
type ProbeResult struct {
	Status  int
	Latency time.Duration
}

// ProbeHTTP gets target with hc and checks the status: expect, or any below 400
// when expect is 0. The result is set whenever a response arrived, even with
// the wrong status. ctx bounds the whole request.
func ProbeHTTP(ctx context.Context, hc *http.Client, target string, expect int) (ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	req.Header.Set("User-Agent", "kipitiny-probe")
	start := time.Now()
	res, err := hc.Do(req)
	if err != nil {
		return ProbeResult{}, err
	}
	// Some servers only count a request once its body is read.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	r := ProbeResult{Status: res.StatusCode, Latency: time.Since(start)}
	switch {
	case expect != 0 && res.StatusCode != expect:
		return r, fmt.Errorf("status %d, expected %d", res.StatusCode, expect)
	case expect == 0 && res.StatusCode >= 400:
		return r, fmt.Errorf("status %d", res.StatusCode)
	}
	return r, nil
}

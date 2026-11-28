package main

// `kipitiny probe`: the healthcheck the manager injects into app containers,
// whose images may have no curl or wget.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

const probeTimeout = 3 * time.Second

// probeCheck succeeds when tcp://host:port accepts a connection, or when an
// http:// URL answers with a status below 400.
func probeCheck(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	switch u.Scheme {
	case "tcp":
		conn, err := new(net.Dialer).DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return err
		}
		return conn.Close()
	case "http":
		_, err := monitoring.ProbeHTTP(ctx, monitoring.NewProbeClient(), target, 0)
		return err
	default:
		return fmt.Errorf("unsupported probe %q", target)
	}
}

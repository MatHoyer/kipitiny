package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// deployCmd deploys a service through a manager's API and waits for the
// outcome, for CI: `kipitiny deploy --service shop/web --tag sha-abc`.
// It exits non-zero when the deployment fails.
func deployCmd() error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	cl := deployClient{interval: 2 * time.Second, out: os.Stdout}
	var opts deployOptions
	fs.StringVar(&cl.base, "url", os.Getenv("KIPITINY_URL"), "manager URL (env KIPITINY_URL)")
	fs.StringVar(&cl.token, "token", os.Getenv("KIPITINY_TOKEN"), "API token with the deploy scope (env KIPITINY_TOKEN)")
	service := fs.String("service", "", "service as project/service, or its ID")
	fs.StringVar(&opts.Tag, "tag", "", "image services: deploy this tag of the service's image")
	fs.StringVar(&opts.Digest, "digest", "", "image services: deploy this digest (sha256:…) of the service's image")
	fs.StringVar(&opts.Commit, "commit", "", "source commit SHA, recorded on the deployment")
	timeout := fs.Duration("timeout", 20*time.Minute, "how long to wait for the outcome")
	noWait := fs.Bool("no-wait", false, "return once the deployment started")
	if err := fs.Parse(os.Args[2:]); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if cl.base == "" || cl.token == "" || *service == "" {
		fs.Usage()
		return errors.New("--url, --token and --service are required")
	}
	cl.base = strings.TrimRight(cl.base, "/")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	return cl.deploy(ctx, *service, opts, !*noWait)
}

// deployOptions mirrors core.DeployOptions.
type deployOptions struct {
	Tag    string `json:"tag,omitempty"`
	Digest string `json:"digest,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type deployment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Image  string `json:"image"`
	Error  string `json:"error"`
}

type deployClient struct {
	base, token string
	http        http.Client
	interval    time.Duration
	out         io.Writer
}

func (c *deployClient) deploy(ctx context.Context, service string, opts deployOptions, wait bool) error {
	id, err := c.resolve(ctx, service)
	if err != nil {
		return err
	}
	var dep deployment
	if err := c.call(ctx, http.MethodPost, "/api/services/"+url.PathEscape(id)+"/deploy", opts, &dep); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Deployment %s started\n", dep.ID)
	if !wait {
		return nil
	}

	printed := 0 // bytes of the log already shown
	for {
		if err := c.call(ctx, http.MethodGet, "/api/deployments/"+dep.ID, nil, &dep); err != nil {
			return err
		}
		if log, err := c.text(ctx, "/api/deployments/"+dep.ID+"/log"); err == nil && len(log) > printed {
			io.WriteString(c.out, log[printed:])
			printed = len(log)
		}
		switch dep.Status {
		case "succeeded":
			fmt.Fprintf(c.out, "Deployed %s\n", dep.Image)
			return nil
		case "failed":
			return fmt.Errorf("deployment %s failed: %s", dep.ID, dep.Error)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("deployment %s still %s: %w", dep.ID, dep.Status, ctx.Err())
		case <-time.After(c.interval):
		}
	}
}

// resolve turns "project/service" into a service ID; anything else is
// taken as an ID.
func (c *deployClient) resolve(ctx context.Context, ref string) (string, error) {
	projectName, name, ok := strings.Cut(ref, "/")
	if !ok {
		return ref, nil
	}
	var projects []struct{ ID, Name string }
	if err := c.call(ctx, http.MethodGet, "/api/projects", nil, &projects); err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Name != projectName {
			continue
		}
		var svcs []struct{ ID, Name string }
		if err := c.call(ctx, http.MethodGet, "/api/projects/"+p.ID+"/services", nil, &svcs); err != nil {
			return "", err
		}
		for _, s := range svcs {
			if s.Name == name {
				return s.ID, nil
			}
		}
	}
	return "", fmt.Errorf("service %s not found", ref)
}

func (c *deployClient) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *deployClient) text(ctx context.Context, path string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func (c *deployClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var e struct{ Error string }
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	return resp, nil
}

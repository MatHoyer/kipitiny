package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

// previewCmd opens, updates or closes a project's preview from CI:
// `kipitiny preview --project shop --name pr-12 --tag web=sha-abc`. It waits
// for the services that changed and prints the preview's URLs; it exits
// non-zero when one fails.
func previewCmd() error {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	cl := deployClient{interval: 2 * time.Second, out: os.Stdout}
	var opts previewOptions
	tags, env := pairs{}, pairs{}
	fs.StringVar(&cl.base, "url", os.Getenv("KIPITINY_URL"), "manager URL (env KIPITINY_URL)")
	fs.StringVar(&cl.token, "token", os.Getenv("KIPITINY_TOKEN"), "API token with the deploy permission (env KIPITINY_TOKEN)")
	project := fs.String("project", "", "the project previewed, by name or ID")
	name := fs.String("name", "", "the preview's name, e.g. pr-12 (lowercase letters, digits, dashes)")
	fs.Var(tags, "tag", "service=tag (or service=sha256:…) to deploy; repeat per service; others keep the project's image")
	fs.Var(env, "env", "KEY=value project variable for this preview only (plain text); repeatable")
	fs.StringVar(&opts.Branch, "branch", "", "source branch: a git-linked project's compose file is read there")
	fs.StringVar(&opts.Commit, "commit", "", "source commit SHA, recorded on the deployments")
	ttl := fs.Duration("ttl", 0, "close the preview after this long unused (default: the project's preview settings, else 7 days)")
	fs.BoolVar(&opts.Seed, "seed", false, "fill the databases a new preview gets with the project's latest backups, anonymized or as is, as the preview settings say")
	closing := fs.Bool("close", false, "close the preview: delete its containers, volumes and databases")
	timeout := fs.Duration("timeout", 20*time.Minute, "how long to wait for the outcome")
	noWait := fs.Bool("no-wait", false, "return once the deploys started")
	if err := fs.Parse(os.Args[2:]); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if cl.base == "" || cl.token == "" || *project == "" || *name == "" {
		fs.Usage()
		return errors.New("--url, --token, --project and --name are required")
	}
	cl.base = strings.TrimRight(cl.base, "/")
	opts.Tags, opts.Env, opts.TTLSeconds = tags, env, int(ttl.Seconds())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	id, err := cl.projectID(ctx, *project)
	if err != nil {
		return err
	}
	path := "/api/projects/" + url.PathEscape(id) + "/previews/" + url.PathEscape(*name)
	if *closing {
		resp, err := cl.do(ctx, http.MethodDelete, path, nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
		fmt.Fprintf(cl.out, "Preview %s closed\n", *name)
		return nil
	}
	return cl.preview(ctx, path, opts, !*noWait)
}

// previewOptions mirrors core.PreviewOptions.
type previewOptions struct {
	Tags       map[string]string `json:"tags,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Commit     string            `json:"commit,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	TTLSeconds int               `json:"ttlSeconds,omitempty"`
	Seed       bool              `json:"seed,omitempty"`
}

type previewView struct {
	ID      string `json:"id"`
	Preview struct {
		Name string `json:"name"`
	} `json:"preview"`
	URLs map[string]string `json:"urls"`
	Plan struct {
		Deploying []string `json:"deploying"`
		Warnings  []string `json:"warnings"`
	} `json:"plan"`
}

type svcRef struct{ ID, Name string }

func (c *deployClient) preview(ctx context.Context, path string, opts previewOptions, wait bool) error {
	// The latest deployment of each service before, to know the new ones.
	before := map[string]string{}
	var existing []previewView
	if err := c.call(ctx, http.MethodGet, path[:strings.LastIndex(path, "/")], nil, &existing); err != nil {
		return err
	}
	name := path[strings.LastIndex(path, "/")+1:]
	for _, p := range existing {
		if p.Preview.Name == name {
			if err := c.latestDeployments(ctx, p.ID, before); err != nil {
				return err
			}
		}
	}

	var v previewView
	if err := c.call(ctx, http.MethodPut, path, opts, &v); err != nil {
		return err
	}
	for _, w := range v.Plan.Warnings {
		fmt.Fprintf(c.out, "warning: %s\n", w)
	}
	if len(v.Plan.Deploying) == 0 {
		fmt.Fprintln(c.out, "Nothing changed")
	} else {
		fmt.Fprintf(c.out, "Deploying %s\n", strings.Join(v.Plan.Deploying, ", "))
	}
	if wait && len(v.Plan.Deploying) > 0 {
		if err := c.waitPreview(ctx, v.ID, v.Plan.Deploying, before); err != nil {
			return err
		}
	}
	for _, svc := range slices.Sorted(maps.Keys(v.URLs)) {
		fmt.Fprintf(c.out, "%s: %s\n", svc, v.URLs[svc])
	}
	return nil
}

// latestDeployments records each service's latest deployment ID by name.
func (c *deployClient) latestDeployments(ctx context.Context, projectID string, into map[string]string) error {
	var svcs []svcRef
	if err := c.call(ctx, http.MethodGet, "/api/projects/"+projectID+"/services", nil, &svcs); err != nil {
		return err
	}
	for _, s := range svcs {
		var deps []deployment
		if err := c.call(ctx, http.MethodGet, "/api/services/"+s.ID+"/deployments", nil, &deps); err != nil {
			return err
		}
		if len(deps) > 0 {
			into[s.Name] = deps[0].ID
		}
	}
	return nil
}

// waitPreview waits until each named service has a finished deployment
// newer than before's.
func (c *deployClient) waitPreview(ctx context.Context, projectID string, names []string, before map[string]string) error {
	pending := slices.Clone(names)
	var failed []string
	for len(pending) > 0 {
		var svcs []svcRef
		if err := c.call(ctx, http.MethodGet, "/api/projects/"+projectID+"/services", nil, &svcs); err != nil {
			return err
		}
		for _, s := range svcs {
			if !slices.Contains(pending, s.Name) {
				continue
			}
			var deps []deployment
			if err := c.call(ctx, http.MethodGet, "/api/services/"+s.ID+"/deployments", nil, &deps); err != nil {
				return err
			}
			if len(deps) == 0 || deps[0].ID == before[s.Name] || deps[0].Status == "running" {
				continue
			}
			pending = slices.DeleteFunc(pending, func(n string) bool { return n == s.Name })
			if deps[0].Status == "failed" {
				failed = append(failed, fmt.Sprintf("%s: %s", s.Name, deps[0].Error))
				fmt.Fprintf(c.out, "%s failed: %s\n", s.Name, deps[0].Error)
			} else {
				fmt.Fprintf(c.out, "%s deployed %s\n", s.Name, deps[0].Image)
			}
		}
		if len(pending) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still deploying %s: %w", strings.Join(pending, ", "), ctx.Err())
		case <-time.After(c.interval):
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("preview deploy failed: %s", strings.Join(failed, "; "))
	}
	return nil
}

// projectID turns a project name into its ID; anything else is taken as
// an ID.
func (c *deployClient) projectID(ctx context.Context, ref string) (string, error) {
	var projects []struct{ ID, Name string }
	if err := c.call(ctx, http.MethodGet, "/api/projects", nil, &projects); err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Name == ref || p.ID == ref {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %s not found", ref)
}

// pairs is a repeatable KEY=value flag.
type pairs map[string]string

func (p pairs) String() string { return "" }

func (p pairs) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("%q: want KEY=value", v)
	}
	p[k] = val
	return nil
}

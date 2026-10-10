package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/templates"
)

// TestTemplatesDocker installs every template with sample inputs on a
// throwaway Docker daemon, checks that each of its services deploys and
// stays up, then deletes it. Slow (it pulls and starts real apps), so CI
// runs it only when templates change, and weekly (templates workflow).
//
//	KIPITINY_TEST_TEMPLATES=1 go test ./internal/core/ -run TemplatesDocker -timeout 30m
//
// It refuses a daemon that already runs kipitiny containers: its apps would
// be routed by that manager's Traefik and removed by its reconciler. Point
// DOCKER_HOST at a docker:dind container to run it on a machine that has one.
func TestTemplatesDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_TEMPLATES") == "" {
		t.Skip("KIPITINY_TEST_TEMPLATES not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	if cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelManaged: "true"}); err != nil {
		t.Fatal(err)
	} else if len(cts) > 0 {
		t.Fatalf("this Docker daemon runs %d kipitiny container(s): use a throwaway one (DOCKER_HOST)", len(cts))
	}
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)
	// The injected health probe is the running binary, here the test's,
	// which can't probe: apps are ready on their own healthcheck, or once
	// up for a few seconds.
	c.probes.Store(store.LocalServerID, (*mount.Mount)(nil))
	t.Cleanup(c.cancel)
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin})

	for _, tpl := range templates.List() {
		t.Run(tpl.ID, func(t *testing.T) {
			res, err := c.InstallTemplate(ctx, tpl.ID, TemplateInstall{
				NewProject: &NewProject{Name: "smoke-" + tpl.ID},
				Values:     smokeValues(t, tpl),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.DeleteProject(ctx, res.ProjectID, "smoke-"+tpl.ID); err != nil {
					t.Errorf("delete: %v", err)
				}
			})
			svcs, err := c.store.ListServices(ctx, res.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range svcs {
				waitDeployed(t, c, s)
			}
			// Deploys wait for a healthcheck or a few stable seconds: give a
			// late crash (bad config read after start) a chance to show.
			time.Sleep(15 * time.Second)
			for _, s := range svcs {
				cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelService: s.ID})
				if err != nil {
					t.Fatal(err)
				}
				if len(cts) == 0 {
					t.Errorf("%s: no container", s.Name)
				}
				for _, ct := range cts {
					res, err := dk.ContainerInspect(ctx, ct.ID, client.ContainerInspectOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if st := res.Container.State; !st.Running || res.Container.RestartCount > 0 || (st.Health != nil && st.Health.Status == container.Unhealthy) {
						t.Errorf("%s: %s, %d restart(s)", s.Name, st.Status, res.Container.RestartCount)
						copyContainerLogs(ctx, dk, []string{ct.ID}, testWriter{t})
					}
				}
			}
		})
	}
}

// waitDeployed waits for the service's deployment to finish, and fails with
// its log unless it succeeded.
func waitDeployed(t *testing.T, c *Core, s store.Service) {
	t.Helper()
	ctx := context.Background()
	for deadline := time.Now().Add(15 * time.Minute); ; time.Sleep(2 * time.Second) {
		deps, err := c.store.ListDeployments(ctx, s.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(deps) == 1 && deps[0].Status != store.DeploymentRunning {
			if deps[0].Status != store.DeploymentSucceeded {
				if rc, err := c.DeploymentLog(ctx, deps[0].ID); err == nil {
					log, _ := io.ReadAll(rc)
					rc.Close()
					t.Logf("%s deploy log:\n%s", s.Name, log)
				}
				t.Fatalf("%s: deploy %s", s.Name, deps[0].Status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not deployed after 15 minutes", s.Name)
		}
	}
}

// smokeValues fills a template's required inputs, leaving the optional ones
// to their defaults: what a user installing it with the least effort gives.
func smokeValues(t *testing.T, tpl templates.Template) map[string]string {
	values := map[string]string{}
	for _, in := range tpl.Inputs {
		if !in.Required || in.Default != "" {
			continue
		}
		switch in.Type {
		case "domain":
			values[in.Name] = tpl.ID + ".localhost" // no certificate asked
		case "url":
			values[in.Name] = "https://" + tpl.ID + ".localhost"
		case "select":
			values[in.Name] = in.Options[0]
		case "checkbox":
			values[in.Name] = "true"
		default:
			values[in.Name] = "smoke-test"
		}
	}
	// The Beszel agent refuses to start without a valid public key.
	if tpl.ID == "beszel-agent" {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		values["KEY"] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	return values
}

// testWriter writes to the test's log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

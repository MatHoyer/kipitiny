package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxDependencies = 20
	// dependencyTimeout is how long waking waits for a service others
	// depend on, the starting page's limit; the next one starts anyway.
	dependencyTimeout = 2 * time.Minute
)

// validateDependsOn checks an app's dependencies. Their names aren't
// checked against the project: a compose file is checked whole, and a
// dependency on a deleted service is skipped.
func validateDependsOn(s store.Service) error {
	if len(s.DependsOn) > maxDependencies {
		return fmt.Errorf("%w: at most %d dependencies", ErrInvalid, maxDependencies)
	}
	seen := map[string]bool{}
	for _, d := range s.DependsOn {
		switch {
		case !nameRe.MatchString(d.Service):
			return fmt.Errorf("%w: depends on %q: not a service name", ErrInvalid, d.Service)
		case d.Service == s.Name:
			return fmt.Errorf("%w: a service can't depend on itself", ErrInvalid)
		case seen[d.Service]:
			return fmt.Errorf("%w: depends on %s twice", ErrInvalid, d.Service)
		case d.Condition != "" && d.Condition != "healthy" && d.Condition != "completed":
			return fmt.Errorf("%w: depends on %s: condition must be healthy or completed", ErrInvalid, d.Service)
		}
		seen[d.Service] = true
	}
	return nil
}

// startOrder sorts services so each comes after those it depends on,
// databases first. A cycle or a missing service doesn't hold the others.
func startOrder(svcs []store.Service) []store.Service {
	byName := map[string]store.Service{}
	for _, s := range svcs {
		byName[s.Name] = s
	}
	out := make([]store.Service, 0, len(svcs))
	seen := map[string]bool{}
	var visit func(s store.Service)
	visit = func(s store.Service) {
		if seen[s.Name] {
			return
		}
		seen[s.Name] = true
		for _, d := range s.DependsOn {
			if dep, ok := byName[d.Service]; ok {
				visit(dep)
			}
		}
		out = append(out, s)
	}
	for _, db := range []bool{true, false} {
		for _, s := range svcs {
			if s.Kind.IsDatabase() == db {
				visit(s)
			}
		}
	}
	return out
}

// awaited is, per service, what the others wait for before they start:
// "healthy" or "completed" ("" when nothing waits but its start).
// Databases are always awaited healthy.
func awaited(svcs []store.Service) map[string]string {
	out := map[string]string{}
	for _, s := range svcs {
		if s.Kind.IsDatabase() {
			out[s.Name] = "healthy"
		}
		for _, d := range s.DependsOn {
			if d.Condition == "completed" || out[d.Service] == "" {
				out[d.Service] = d.Condition
			}
		}
	}
	return out
}

// waitCondition waits until a container meets a dependency condition,
// at most dependencyTimeout.
func (c *Core) waitCondition(ctx context.Context, dk *docker.Client, id string, svc store.Service, cond string) error {
	ctx, cancel := context.WithTimeout(ctx, dependencyTimeout)
	defer cancel()
	switch cond {
	case "healthy":
		return c.waitReady(ctx, dk, id, svc.Runtime.Restart)
	case "completed":
		wait := dk.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
		select {
		case res := <-wait.Result:
			if res.StatusCode != 0 {
				return fmt.Errorf("exited with code %d", res.StatusCode)
			}
			return nil
		case err := <-wait.Error:
			return err
		}
	}
	return nil
}

// isJob reports whether svc runs once (restart "no" or on-failure)
// rather than being kept running.
func isJob(svc store.Service) bool { return svc.Runtime.Restart != "" }

// completed reports whether a container exited with code 0.
func completed(ct container.Summary) bool {
	return ct.State == container.StateExited && strings.HasPrefix(ct.Status, "Exited (0)")
}

// failedDependency names the first dependency of svc deployed in this run
// whose deployment didn't succeed, among those it must wait to be healthy or
// completed (as Compose doesn't start a service after such a failure).
func (c *Core) failedDependency(ctx context.Context, svc store.Service, deployed map[string]string) string {
	for _, d := range svc.DependsOn {
		id, ok := deployed[d.Service]
		if !ok || d.Condition == "" {
			continue
		}
		if dep, err := c.store.GetDeployment(ctx, id); err != nil || dep.Status != store.DeploymentSucceeded {
			return d.Service
		}
	}
	return ""
}

// skipDeploy records a deployment of svc that wasn't tried, with why.
func (c *Core) skipDeploy(ctx context.Context, svc store.Service, commit, why string) (store.Deployment, error) {
	d, err := c.store.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentRunning, Image: svc.Image, GitCommit: commit, Config: svc})
	if err != nil {
		return store.Deployment{}, err
	}
	if f, err := c.createDeployLog(svc.ID, d.ID); err == nil {
		fmt.Fprintf(f, "[%s] Skipped: %s\n", time.Now().UTC().Format(time.TimeOnly), why)
		f.Close()
	}
	d.Status, d.Error = store.DeploymentSkipped, why
	return d, c.store.FinishDeployment(ctx, d.ID, store.DeploymentSkipped, why)
}

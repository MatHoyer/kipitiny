package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/distribution/reference"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
)

type RegistryInput struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	// Password equal to SecretMask keeps the stored value on update.
	Password string `json:"password"`
}

func (c *Deployer) ListRegistries(ctx context.Context) ([]domain.Registry, error) {
	rs, err := c.store.ListRegistries(ctx)
	for i := range rs {
		rs[i].Password = identity.SecretMask
		if identity.IsMember(ctx) {
			rs[i].Username = ""
		}
	}
	return rs, err
}

// CreateRegistry saves a credential once the registry accepts it.
func (c *Deployer) CreateRegistry(ctx context.Context, in RegistryInput) (domain.Registry, error) {
	r := domain.Registry{}
	if err := c.applyRegistry(ctx, &r, in); err != nil {
		return domain.Registry{}, err
	}
	r, err := c.store.CreateRegistry(ctx, r)
	r.Password = identity.SecretMask
	return r, err
}

func (c *Deployer) UpdateRegistry(ctx context.Context, id string, in RegistryInput) (domain.Registry, error) {
	r, err := c.store.GetRegistry(ctx, id)
	if err != nil {
		return domain.Registry{}, err
	}
	if err := c.applyRegistry(ctx, &r, in); err != nil {
		return domain.Registry{}, err
	}
	r, err = c.store.UpdateRegistry(ctx, r)
	r.Password = identity.SecretMask
	return r, err
}

func (c *Deployer) DeleteRegistry(ctx context.Context, id string) error {
	return c.store.DeleteRegistry(ctx, id)
}

// TestRegistry signs in again with the saved credential.
func (c *Deployer) TestRegistry(ctx context.Context, id string) error {
	r, err := c.store.GetRegistry(ctx, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	progress.Report(ctx, "login")
	if err := c.mgr.DockerFor(domain.LocalServerID).Login(ctx, loginAddress(r.Host), r.Username, r.Password); err != nil {
		return fmt.Errorf("%w: %s refused the credentials: %v", ErrInvalid, r.Host, err)
	}
	return nil
}

func (c *Deployer) applyRegistry(ctx context.Context, r *domain.Registry, in RegistryInput) error {
	r.Host = normalizeRegistryHost(in.Host)
	r.Username = strings.TrimSpace(in.Username)
	if in.Password != identity.SecretMask {
		r.Password = strings.TrimSpace(in.Password)
	}
	if r.Host == "" || r.Username == "" || r.Password == "" {
		return fmt.Errorf("%w: host, username and password are required", ErrInvalid)
	}
	if strings.ContainsAny(r.Host, " /") {
		return fmt.Errorf("%w: %q is not a registry host, e.g. ghcr.io", ErrInvalid, r.Host)
	}
	// The manager's Docker signs in, as `docker login` would.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.mgr.DockerFor(domain.LocalServerID).Login(ctx, loginAddress(r.Host), r.Username, r.Password); err != nil {
		return fmt.Errorf("%w: %s refused the credentials: %v", ErrInvalid, r.Host, err)
	}
	return nil
}

// RegistryAuth is the credential to pull image with, if its registry has
// one ("" otherwise: an anonymous pull).
func (c *Deployer) RegistryAuth(ctx context.Context, image string) string {
	host := imageRegistry(image)
	if host == "" {
		return ""
	}
	rs, err := c.store.ListRegistries(ctx)
	if err != nil {
		c.log.Warn("registry credentials unavailable", "err", err)
		return ""
	}
	for _, r := range rs {
		if r.Host == host {
			return domain.RegistryAuth(loginAddress(host), r.Username, r.Password)
		}
	}
	return ""
}

// pullHint points a refused pull at the registry settings.
func pullHint(err error) string {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"denied", "unauthorized", "docker login", "authentication required"} {
		if strings.Contains(msg, s) {
			return " (a private image? add its registry in Integrations › Registries)"
		}
	}
	return ""
}

// imageRegistry is the registry host of an image reference: docker.io for
// Docker Hub ("postgres:17", "org/app").
func imageRegistry(image string) string {
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return ""
	}
	return reference.Domain(ref)
}

// normalizeRegistryHost accepts what people paste (a URL, Docker Hub's
// aliases) and returns the host as image references name it.
func normalizeRegistryHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(strings.TrimSuffix(h, "/v1/"), "/")
	switch h {
	case "index.docker.io", "registry-1.docker.io", "hub.docker.com", "registry.hub.docker.com":
		return "docker.io"
	}
	return h
}

// loginAddress is the server address Docker uses for a host: Docker Hub
// keeps its historical one.
func loginAddress(host string) string {
	if host == "docker.io" {
		return "https://index.docker.io/v1/"
	}
	return host
}

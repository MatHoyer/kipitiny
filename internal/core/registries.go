package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/distribution/reference"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type RegistryInput struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	// Password equal to SecretMask keeps the stored value on update.
	Password string `json:"password"`
}

func (c *Core) ListRegistries(ctx context.Context) ([]store.Registry, error) {
	rs, err := c.store.ListRegistries(ctx)
	for i := range rs {
		rs[i].Password = SecretMask
	}
	return rs, err
}

// CreateRegistry saves a credential once the registry accepts it.
func (c *Core) CreateRegistry(ctx context.Context, in RegistryInput) (store.Registry, error) {
	r := store.Registry{}
	if err := c.applyRegistry(ctx, &r, in); err != nil {
		return store.Registry{}, err
	}
	r, err := c.store.CreateRegistry(ctx, r)
	r.Password = SecretMask
	return r, err
}

func (c *Core) UpdateRegistry(ctx context.Context, id string, in RegistryInput) (store.Registry, error) {
	r, err := c.store.GetRegistry(ctx, id)
	if err != nil {
		return store.Registry{}, err
	}
	if err := c.applyRegistry(ctx, &r, in); err != nil {
		return store.Registry{}, err
	}
	r, err = c.store.UpdateRegistry(ctx, r)
	r.Password = SecretMask
	return r, err
}

func (c *Core) DeleteRegistry(ctx context.Context, id string) error {
	return c.store.DeleteRegistry(ctx, id)
}

// TestRegistry signs in again with the saved credential.
func (c *Core) TestRegistry(ctx context.Context, id string) error {
	r, err := c.store.GetRegistry(ctx, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.pool.Local().Login(ctx, loginAddress(r.Host), r.Username, r.Password); err != nil {
		return fmt.Errorf("%w: %s refused the credentials: %v", ErrInvalid, r.Host, err)
	}
	return nil
}

func (c *Core) applyRegistry(ctx context.Context, r *store.Registry, in RegistryInput) error {
	r.Host = normalizeRegistryHost(in.Host)
	r.Username = strings.TrimSpace(in.Username)
	if in.Password != SecretMask {
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
	if err := c.pool.Local().Login(ctx, loginAddress(r.Host), r.Username, r.Password); err != nil {
		return fmt.Errorf("%w: %s refused the credentials: %v", ErrInvalid, r.Host, err)
	}
	return nil
}

// registryAuth is the credential to pull image with, if its registry has
// one ("" otherwise: an anonymous pull).
func (c *Core) registryAuth(ctx context.Context, image string) string {
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
			return docker.RegistryAuth(loginAddress(host), r.Username, r.Password)
		}
	}
	return ""
}

// pullHint points a refused pull at the registry settings.
func pullHint(err error) string {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"denied", "unauthorized", "docker login", "authentication required"} {
		if strings.Contains(msg, s) {
			return " (a private image? add its registry in Settings › Registries)"
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

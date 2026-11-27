package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// ListDomains returns the base domains offered for service domains.
func (c *Network) ListDomains(ctx context.Context) ([]domain.Domain, error) {
	return c.store.ListDomains(ctx)
}

// CreateDomain registers a base domain (example.com). It changes no routing:
// services still hold their full domain.
func (c *Network) CreateDomain(ctx context.Context, name string) (domain.Domain, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if !domain.ValidDomain(name) {
		return domain.Domain{}, fmt.Errorf("%w: %q is not a valid domain name", domain.ErrInvalid, name)
	}
	d, err := c.store.CreateDomain(ctx, domain.Domain{Name: name})
	if errors.Is(err, domain.ErrConflict) {
		return domain.Domain{}, fmt.Errorf("%w: %s is already listed", domain.ErrInvalid, name)
	}
	return d, err
}

// DeleteDomain removes a base domain from the list; services using it keep
// their domain.
func (c *Network) DeleteDomain(ctx context.Context, id string) error {
	return c.store.DeleteDomain(ctx, id)
}

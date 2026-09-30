package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// ListDomains returns the base domains offered for service domains.
func (c *Core) ListDomains(ctx context.Context) ([]store.Domain, error) {
	return c.store.ListDomains(ctx)
}

// CreateDomain registers a base domain (example.com). It changes no routing:
// services still hold their full domain.
func (c *Core) CreateDomain(ctx context.Context, name string) (store.Domain, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if !domainRe.MatchString(name) {
		return store.Domain{}, fmt.Errorf("%w: %q is not a valid domain name", ErrInvalid, name)
	}
	d, err := c.store.CreateDomain(ctx, store.Domain{Name: name})
	if errors.Is(err, store.ErrConflict) {
		return store.Domain{}, fmt.Errorf("%w: %s is already listed", ErrInvalid, name)
	}
	return d, err
}

// DeleteDomain removes a base domain from the list; services using it keep
// their domain.
func (c *Core) DeleteDomain(ctx context.Context, id string) error {
	return c.store.DeleteDomain(ctx, id)
}

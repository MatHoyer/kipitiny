package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/MatHoyer/kipitiny/internal/analytics"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// sharedHidden are the breakdowns a shared link leaves out: who the app
// said its visitors are, and the custom events' properties, which may name
// them too.
var sharedHidden = []string{"identities", "props"}

func shareHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ShareServiceAnalytics creates the app's public, read-only analytics link
// and returns its token, shown only now. One link per app: a new one
// revokes the one before.
func (c *Core) ShareServiceAnalytics(ctx context.Context, serviceID string) (string, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if !svc.Analytics {
		return "", fmt.Errorf("%w: analytics are off for %s (its settings)", ErrInvalid, svc.Name)
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, c.store.SetServiceAnalyticsShare(ctx, serviceID, shareHash(token))
}

// UnshareServiceAnalytics revokes the app's analytics link.
func (c *Core) UnshareServiceAnalytics(ctx context.Context, serviceID string) error {
	return c.store.SetServiceAnalyticsShare(ctx, serviceID, "")
}

// SharedAnalyticsView is what a shared link shows: the domain and its
// stats, without identities, properties, visits or settings.
type SharedAnalyticsView struct {
	Domain string `json:"domain"`
	analytics.Stats
}

// SharedAnalytics answers a shared link, without a session: its token is
// the credential. Filters are ignored.
func (c *Core) SharedAnalytics(ctx context.Context, token string, q AnalyticsQuery) (SharedAnalyticsView, error) {
	svc, err := c.store.GetServiceByAnalyticsShare(ctx, shareHash(token))
	if err != nil {
		return SharedAnalyticsView{}, err
	}
	if !svc.Analytics || svc.Domain == "" {
		return SharedAnalyticsView{}, store.ErrNotFound
	}
	cl, _, err := c.analyticsOn(ctx)
	if err != nil {
		return SharedAnalyticsView{}, err
	}
	q.Filters = nil
	aq, err := q.since(svc).query(svc.Domain)
	if err != nil {
		return SharedAnalyticsView{}, err
	}
	s, err := cl.Stats(ctx, aq)
	if err != nil {
		return SharedAnalyticsView{}, err
	}
	for _, k := range sharedHidden {
		delete(s.Breakdowns, k)
	}
	return SharedAnalyticsView{Domain: svc.Domain, Stats: s}, nil
}

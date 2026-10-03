package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// ErrForbidden means the caller is authenticated but its scope is too small.
var ErrForbidden = errors.New("forbidden")

// Actor is who performs a request: a signed-in user (full access) or an API
// token (limited to its scope).
type Actor struct {
	Kind  string      `json:"kind"` // "user", "token" or "webhook"
	Name  string      `json:"name"`
	Scope store.Scope `json:"scope"`
}

func (a Actor) String() string { return a.Kind + ":" + a.Name }

type actorKey struct{}

func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// Require fails unless the context's actor holds scope want.
func Require(ctx context.Context, want store.Scope) error {
	a, ok := ActorFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !a.Scope.Allows(want) {
		return fmt.Errorf("%w: needs the %s scope", ErrForbidden, want)
	}
	return nil
}

// tokenPrefix makes tokens recognisable (e.g. by secret scanners).
const tokenPrefix = "kpt_"

type NewToken struct {
	store.APIToken
	// Token is shown once, at creation.
	Token string `json:"token"`
}

func (c *Core) CreateAPIToken(ctx context.Context, name string, scope store.Scope) (NewToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return NewToken{}, fmt.Errorf("%w: give the token a name (max 60 characters)", ErrInvalid)
	}
	if !scope.Allows(store.ScopeRead) {
		return NewToken{}, fmt.Errorf("%w: scope must be read, deploy or admin", ErrInvalid)
	}
	token := tokenPrefix + randomToken(32)
	t, err := c.store.CreateAPIToken(ctx, store.APIToken{Name: name, Scope: scope, TokenHash: hashToken(token)})
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{APIToken: t, Token: token}, nil
}

func (c *Core) ListAPITokens(ctx context.Context) ([]store.APIToken, error) {
	return c.store.ListAPITokens(ctx)
}

func (c *Core) DeleteAPIToken(ctx context.Context, id string) error {
	return c.store.DeleteAPIToken(ctx, id)
}

// AuthenticateToken resolves a bearer token to its actor.
func (c *Core) AuthenticateToken(ctx context.Context, token string) (Actor, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return Actor{}, ErrUnauthorized
	}
	t, err := c.store.GetAPITokenByHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return Actor{}, ErrUnauthorized
	}
	if err != nil {
		return Actor{}, err
	}
	return Actor{Kind: "token", Name: t.Name, Scope: t.Scope}, nil
}

const auditRetention = 90 * 24 * time.Hour

// Audit records an action by the context's actor. Failures to record are
// logged, never returned: auditing must not break the action.
func (c *Core) Audit(ctx context.Context, action, target string, status int, actionErr error) {
	a, _ := ActorFrom(ctx)
	e := store.AuditEntry{Actor: a.String(), Action: action, Target: target, Status: status}
	if actionErr != nil {
		e.Error = actionErr.Error()
	}
	if err := c.store.AddAudit(context.WithoutCancel(ctx), e); err != nil {
		c.log.Warn("cannot record audit entry", "action", action, "err", err)
	}
}

func (c *Core) ListAudit(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return c.store.ListAudit(ctx, limit)
}

func (c *Core) pruneAudit(ctx context.Context) {
	if err := c.store.PruneAudit(ctx, time.Now().Add(-auditRetention)); err != nil {
		c.log.Warn("cannot prune audit log", "err", err)
	}
}

// ResolveService finds a service by ID or by "project/service" name.
func (c *Core) ResolveService(ctx context.Context, ref string) (store.Service, error) {
	ref = strings.TrimSpace(ref)
	if projectName, name, ok := strings.Cut(ref, "/"); ok {
		projects, err := c.store.ListProjects(ctx)
		if err != nil {
			return store.Service{}, err
		}
		for _, p := range projects {
			if p.Name != projectName {
				continue
			}
			svcs, err := c.store.ListServices(ctx, p.ID)
			if err != nil {
				return store.Service{}, err
			}
			for _, s := range svcs {
				if s.Name == name {
					return s, nil
				}
			}
		}
		return store.Service{}, fmt.Errorf("%w: no service %q (use project/service)", store.ErrNotFound, ref)
	}
	return c.store.GetService(ctx, ref)
}

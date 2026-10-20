package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// ErrForbidden means the caller is authenticated but may not do that.
var ErrForbidden = errors.New("forbidden")

// Actor is who performs a request: a signed-in user or an API token acting
// for its owner. A token never does more than its owner may.
type Actor struct {
	Kind string `json:"kind"` // "user" or "token"
	Name string `json:"name"`
	// UserID is the signed-in user's, or the token owner's.
	UserID string `json:"-"`
	// Role, Features and Grants are the user's (the token owner's).
	Role     store.Role `json:"role"`
	Features []string   `json:"-"`
	// Grants maps project IDs to a member's permissions; nil for admins.
	Grants map[string][]string `json:"-"`
	// Session is the hash of the session's token; empty for tokens.
	Session string `json:"-"`
	// TokenID is the restricted token's that acts; empty otherwise.
	TokenID string `json:"-"`
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

// Authenticated fails unless the context has an actor.
func Authenticated(ctx context.Context) error {
	if _, ok := ActorFrom(ctx); !ok {
		return ErrUnauthorized
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

// TokenInput is a new token: its name, and when Restricted the
// features and project permissions it gets, within its creator's.
type TokenInput struct {
	Name       string              `json:"name"`
	Restricted bool                `json:"restricted"`
	Features   []string            `json:"features"`
	Grants     map[string][]string `json:"grants"`
}

func (c *Core) CreateAPIToken(ctx context.Context, in TokenInput) (NewToken, error) {
	a, ok := ActorFrom(ctx)
	if !ok || a.Kind != "user" {
		return NewToken{}, ErrUnauthorized
	}
	t := store.APIToken{UserID: a.UserID}
	if err := c.setTokenAccess(ctx, a, &t, in); err != nil {
		return NewToken{}, err
	}
	token := tokenPrefix + randomToken(32)
	t.TokenHash = hashToken(token)
	t, err := c.store.CreateAPIToken(ctx, t)
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{APIToken: t, Token: token}, nil
}

// UpdateAPIToken renames a token and changes its access; a member only their own.
func (c *Core) UpdateAPIToken(ctx context.Context, id string, in TokenInput) (store.APIToken, error) {
	a, t, err := c.ownToken(ctx, id)
	if err != nil {
		return store.APIToken{}, err
	}
	if err := c.setTokenAccess(ctx, a, &t, in); err != nil {
		return store.APIToken{}, err
	}
	return c.store.UpdateAPIToken(ctx, t)
}

// RollAPIToken replaces a token's secret, keeping its name and access: the
// old secret stops working immediately.
func (c *Core) RollAPIToken(ctx context.Context, id string) (NewToken, error) {
	_, t, err := c.ownToken(ctx, id)
	if err != nil {
		return NewToken{}, err
	}
	token := tokenPrefix + randomToken(32)
	t.TokenHash = hashToken(token)
	if t, err = c.store.UpdateAPIToken(ctx, t); err != nil {
		return NewToken{}, err
	}
	return NewToken{APIToken: t, Token: token}, nil
}

// ownToken is token id, when the signed-in user may manage it.
func (c *Core) ownToken(ctx context.Context, id string) (Actor, store.APIToken, error) {
	a, ok := ActorFrom(ctx)
	if !ok || a.Kind != "user" {
		return Actor{}, store.APIToken{}, ErrUnauthorized
	}
	ts, err := c.ListAPITokens(ctx)
	if err != nil {
		return Actor{}, store.APIToken{}, err
	}
	i := slices.IndexFunc(ts, func(t store.APIToken) bool { return t.ID == id })
	if i < 0 {
		return Actor{}, store.APIToken{}, fmt.Errorf("%w: token", store.ErrNotFound)
	}
	return a, ts[i], nil
}

// setTokenAccess validates in and sets it on t: what a restricted token
// gets stays within what a, its creator or editor, holds.
func (c *Core) setTokenAccess(ctx context.Context, a Actor, t *store.APIToken, in TokenInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		return fmt.Errorf("%w: give the token a name (max 60 characters)", ErrInvalid)
	}
	t.Name, t.Restricted, t.Features, t.Grants = name, in.Restricted, []string{}, map[string][]string{}
	if !in.Restricted {
		return nil
	}
	access, err := c.normalized(ctx, UserAccess{Role: store.RoleMember, Features: in.Features, Grants: in.Grants})
	if err != nil {
		return err
	}
	for _, f := range access.Features {
		if !a.Has(Feature(f)) {
			return fmt.Errorf("%w: a token can't see %s when you can't", ErrForbidden, f)
		}
	}
	for projectID, perms := range access.Grants {
		for _, p := range perms {
			if !a.Can(projectID, Perm(p)) {
				return fmt.Errorf("%w: a token can't have the %s permission on a project where you don't", ErrForbidden, p)
			}
		}
	}
	t.Features, t.Grants = access.Features, access.Grants
	return nil
}

// ListAPITokens returns every token to an admin, a member's own to them.
func (c *Core) ListAPITokens(ctx context.Context) ([]store.APIToken, error) {
	a, ok := ActorFrom(ctx)
	if !ok {
		return nil, ErrUnauthorized
	}
	if a.Admin() {
		return c.store.ListAPITokens(ctx, "")
	}
	return c.store.ListAPITokens(ctx, a.UserID)
}

// DeleteAPIToken revokes a token; a member only their own.
func (c *Core) DeleteAPIToken(ctx context.Context, id string) error {
	ts, err := c.ListAPITokens(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ts, func(t store.APIToken) bool { return t.ID == id }) {
		return fmt.Errorf("%w: token", store.ErrNotFound)
	}
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
	a := Actor{Kind: "token", Name: t.Name, Role: store.RoleAdmin}
	// Tokens made before they had owners, when only admins existed, act as one.
	if t.UserID != "" {
		u, err := c.store.GetUser(ctx, t.UserID)
		if err != nil {
			return Actor{}, err
		}
		a.UserID, a.Role, a.Features = u.ID, u.Role, u.Features
		if a, err = c.withGrants(ctx, a); err != nil {
			return Actor{}, err
		}
	}
	if !t.Restricted {
		return a, nil
	}
	return c.restricted(ctx, a, t)
}

// restricted narrows the owner's actor a to what token t was given: it acts
// as a member holding what both have, so an owner losing a permission takes
// it from the token too.
func (c *Core) restricted(ctx context.Context, a Actor, t store.APIToken) (Actor, error) {
	tok := Actor{Role: store.RoleMember, Grants: maps.Clone(t.Grants)}
	if err := c.withPreviews(ctx, tok.Grants); err != nil {
		return Actor{}, err
	}
	out := a
	out.Role, out.Features, out.Grants, out.TokenID = store.RoleMember, []string{}, map[string][]string{}, t.ID
	for _, f := range t.Features {
		if a.Has(Feature(f)) {
			out.Features = append(out.Features, f)
		}
	}
	for projectID := range tok.Grants {
		var perms []string
		for _, p := range Perms {
			if a.Can(projectID, p) && tok.Can(projectID, p) {
				perms = append(perms, string(p))
			}
		}
		if len(perms) > 0 {
			out.Grants[projectID] = perms
		}
	}
	return out, nil
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

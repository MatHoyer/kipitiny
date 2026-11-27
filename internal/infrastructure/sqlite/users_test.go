package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("users = %d", n)
	}
	u, err := s.CreateUser(ctx, domain.User{Username: "admin", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, domain.User{Username: "admin", PasswordHash: "y"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}
	if got, err := s.GetUserByUsername(ctx, "admin"); err != nil || got.ID != u.ID {
		t.Fatalf("by username: %+v %v", got, err)
	}

	future := time.Now().Add(time.Hour)
	if err := s.CreateSession(ctx, domain.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, domain.Session{TokenHash: "dead", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if _, err := s.GetSession(ctx, "dead"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired session should be not found, got %v", err)
	}
	if err := s.SetPassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("password change must revoke sessions, got %v", err)
	}
	if err := s.CreateSession(ctx, domain.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestUserAccessAndInvites(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	admin, err := s.CreateUser(ctx, domain.User{Username: "admin", PasswordHash: "x", Role: domain.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, domain.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvite(ctx, domain.Invite{
		TokenHash: "inv", Role: domain.RoleMember, Features: []string{"storage"},
		Grants:    map[string][]string{p.ID: {"view", "deploy"}, "gone": {"view"}},
		CreatedBy: admin.ID, Email: "alice@example.com", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInvite(ctx, domain.Invite{TokenHash: "old", Role: domain.RoleMember, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if is, err := s.ListInvites(ctx); err != nil || len(is) != 1 || is[0].ID != inv.ID || is[0].Email != "alice@example.com" {
		t.Fatalf("invites: %+v %v", is, err)
	}
	if _, err := s.GetInviteByHash(ctx, "old"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired invite: %v", err)
	}
	got, err := s.GetInviteByHash(ctx, "inv")
	if err != nil || got.Grants[p.ID][1] != "deploy" {
		t.Fatalf("invite by hash: %+v %v", got, err)
	}

	m, err := s.AcceptInvite(ctx, inv.ID, domain.User{Username: "alice", PasswordHash: "y"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != domain.RoleMember || len(m.Features) != 1 {
		t.Fatalf("member: %+v", m)
	}
	if _, err := s.AcceptInvite(ctx, inv.ID, domain.User{Username: "bob", PasswordHash: "y"}, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invite reused: %v", err)
	}
	gs, err := s.ListGrants(ctx, m.ID)
	if err != nil || len(gs) != 1 || gs[0].ProjectID != p.ID || len(gs[0].Perms) != 2 {
		t.Fatalf("grants: %+v %v", gs, err)
	}

	if err := s.SetGrant(ctx, domain.ProjectGrant{UserID: m.ID, ProjectID: p.ID, Perms: []string{"view"}}); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.ListGrants(ctx, m.ID); len(gs) != 1 || len(gs[0].Perms) != 1 {
		t.Fatalf("replaced grant: %+v", gs)
	}
	if err := s.SetGrant(ctx, domain.ProjectGrant{UserID: m.ID, ProjectID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.ListGrants(ctx, m.ID); len(gs) != 0 {
		t.Fatalf("grant without permissions should be gone: %+v", gs)
	}

	u, err := s.UpdateUserAccess(ctx, m.ID, domain.RoleAdmin, nil)
	if err != nil || u.Role != domain.RoleAdmin || u.Features == nil {
		t.Fatalf("update access: %+v %v", u, err)
	}
	if err := s.SetUserPreferences(ctx, m.ID, domain.Preferences{MapView: "3d"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, m.ID); u.Preferences.MapView != "3d" {
		t.Fatalf("preferences: %+v", u.Preferences)
	}
	if err := s.SetUserPreferences(ctx, "nope", domain.Preferences{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if us, err := s.ListUsers(ctx); err != nil || len(us) != 2 || us[0].ID != admin.ID {
		t.Fatalf("users: %+v %v", us, err)
	}

	if _, err := s.CreateAPIToken(ctx, domain.APIToken{Name: "a", TokenHash: "t1", UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIToken(ctx, domain.APIToken{Name: "m", TokenHash: "t2", UserID: m.ID}); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ListAPITokens(ctx, m.ID); len(ts) != 1 || ts[0].Name != "m" {
		t.Fatalf("member tokens: %+v", ts)
	}
	if err := s.CreateSession(ctx, domain.Session{TokenHash: "sess", UserID: m.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ListAPITokens(ctx, ""); len(ts) != 1 {
		t.Fatalf("deleting a user deletes their tokens: %+v", ts)
	}
	if _, err := s.GetSession(ctx, "sess"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleting a user deletes their sessions: %v", err)
	}
}

func TestTokensAndAudit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	tok, err := s.CreateAPIToken(ctx, domain.APIToken{Name: "ci", TokenHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPITokenByHash(ctx, "h1")
	if err != nil || got.ID != tok.ID || got.LastUsedAt == nil {
		t.Fatalf("by hash: %+v %v", got, err)
	}
	if _, err := s.GetAPITokenByHash(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if err := s.DeleteAPIToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"deploy", "stop"} {
		if err := s.AddAudit(ctx, domain.AuditEntry{Actor: "user:admin", Action: a, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	es, err := s.ListAudit(ctx, 10)
	if err != nil || len(es) != 2 || es[0].Action != "stop" {
		t.Fatalf("audit: %+v %v", es, err)
	}
	if err := s.PruneAudit(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if es, _ := s.ListAudit(ctx, 10); len(es) != 0 {
		t.Fatalf("prune: %v", es)
	}
}

func TestPushSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	admin, err := s.CreateUser(ctx, domain.User{Username: "admin", PasswordHash: "x", Role: domain.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(ctx, domain.User{Username: "member", PasswordHash: "x", Role: domain.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SavePushSubscription(ctx, domain.PushSubscription{UserID: admin.ID, Name: "Chrome", Endpoint: "https://e/1", P256dh: "k", Auth: "a"})
	if err != nil {
		t.Fatal(err)
	}
	// the same browser again, signed in as someone else: moved, not duplicated
	again, err := s.SavePushSubscription(ctx, domain.PushSubscription{UserID: member.ID, Name: "Chrome", Endpoint: "https://e/1", P256dh: "k2", Auth: "a2"})
	if err != nil || again.ID != p.ID || again.P256dh != "k2" {
		t.Fatalf("resubscribe: %+v %v", again, err)
	}
	if ps, _ := s.ListAdminPushSubscriptions(ctx); len(ps) != 0 {
		t.Fatalf("admin subscriptions after the move: %+v", ps)
	}
	if _, err := s.SavePushSubscription(ctx, domain.PushSubscription{UserID: admin.ID, Name: "Firefox", Endpoint: "https://e/2", P256dh: "k", Auth: "a"}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.ListAdminPushSubscriptions(ctx); len(ps) != 1 || ps[0].Name != "Firefox" {
		t.Fatalf("admin subscriptions: %+v", ps)
	}
	if err := s.DeletePushSubscription(ctx, admin.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete someone else's: %v", err)
	}
	if err := s.DeletePushSubscription(ctx, member.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.ListPushSubscriptions(ctx, member.ID); len(ps) != 0 {
		t.Fatalf("member subscriptions: %+v", ps)
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
)

// members is a manager with an admin, a member and two projects: shop,
// which tests grant the member permissions on, and other, never granted.
type members struct {
	st     store.Store
	c      *core.Core
	api    *API
	member store.User
	// IDs by route kind, in shop and in other.
	shop, other map[core.IDKind]string
}

func newMembers(t *testing.T) members {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dc, err := docker.New() // lazily connects; these tests refuse before Docker
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{DataDir: t.TempDir()}, st, dc, log)
	m := members{st: st, c: c, api: New(c, log)}
	if _, err := st.CreateUser(ctx, store.User{Username: "admin", PasswordHash: "x", Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if m.member, err = st.CreateUser(ctx, store.User{Username: "alice", PasswordHash: "x", Role: store.RoleMember}); err != nil {
		t.Fatal(err)
	}
	seed := func(name string) map[core.IDKind]string {
		p, err := st.CreateProject(ctx, store.Project{Name: name, ServerID: store.LocalServerID})
		if err != nil {
			t.Fatal(err)
		}
		svc, err := st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx",
			Replicas: 1, Env: map[string]string{"A": "1"}, Secrets: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		d, err := st.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentSucceeded, Image: "nginx"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.CreateBackup(ctx, store.Backup{Kind: store.BackupKindVolume, ServiceID: svc.ID, ProjectID: p.ID, ServiceName: "web",
			ProjectName: name, ServiceKind: store.ServiceKindApp, TargetID: store.LocalTargetID, Status: store.OpSucceeded})
		if err != nil {
			t.Fatal(err)
		}
		sc, err := st.CreateBackupSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindVolume, ServiceID: svc.ID,
			TargetID: store.LocalTargetID, Cron: "@daily", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		job, err := st.CreateJob(ctx, store.Job{ServiceID: svc.ID, Name: "nightly", Command: "true", Cron: "@daily", TimeoutSec: 60})
		if err != nil {
			t.Fatal(err)
		}
		return map[core.IDKind]string{core.IDProject: p.ID, core.IDService: svc.ID, core.IDDeployment: d.ID,
			core.IDBackup: b.ID, core.IDSchedule: sc.ID, core.IDJob: job.ID}
	}
	m.shop, m.other = seed("shop"), seed("other")
	return m
}

// as returns a context acting as the member with these permissions on shop
// and these features.
func (m members) as(t *testing.T, perms []core.Perm, features ...core.Feature) context.Context {
	t.Helper()
	ctx := context.Background()
	ps := make([]string, len(perms))
	for i, p := range perms {
		ps[i] = string(p)
	}
	if err := m.st.SetGrant(ctx, store.ProjectGrant{UserID: m.member.ID, ProjectID: m.shop[core.IDProject], Perms: ps}); err != nil {
		t.Fatal(err)
	}
	fs := make([]string, len(features))
	for i, f := range features {
		fs[i] = string(f)
	}
	u, err := m.st.UpdateUserAccess(ctx, m.member.ID, store.RoleMember, fs)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.c.UserActor(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	return core.WithActor(ctx, a)
}

// token returns an admin-scoped token of the member with these permissions
// on shop.
func (m members) token(t *testing.T, perms ...core.Perm) string {
	t.Helper()
	tok, err := m.c.CreateAPIToken(m.as(t, perms), core.TokenInput{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return tok.Token
}

func (m members) authorize(ctx context.Context, pattern string, kind core.IDKind, ids map[core.IDKind]string) error {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if kind != "" {
		r.SetPathValue("id", ids[kind])
	}
	return m.api.authorize(ctx, pattern, r)
}

// TestMemberRouteAccess checks every route of routeAccess for a member:
// with exactly the permission (or feature) it needs, with every other one,
// and on a project they weren't granted.
func TestMemberRouteAccess(t *testing.T) {
	m := newMembers(t)
	others := func(p core.Perm) []core.Perm {
		return slices.DeleteFunc(slices.Clone(core.Perms), func(q core.Perm) bool { return q == p })
	}
	for pattern, acc := range routeAccess {
		switch {
		case acc.anyone:
			if err := m.authorize(m.as(t, nil), pattern, "", nil); err != nil {
				t.Errorf("%s: refused to a member: %v", pattern, err)
			}
		case acc.feature != "":
			if err := m.authorize(m.as(t, nil, acc.feature), pattern, "", nil); err != nil {
				t.Errorf("%s: refused with %s: %v", pattern, acc.feature, err)
			}
			if err := m.authorize(m.as(t, core.Perms), pattern, "", nil); !errors.Is(err, core.ErrForbidden) {
				t.Errorf("%s: allowed without %s: %v", pattern, acc.feature, err)
			}
		case acc.perm != "":
			if err := m.authorize(m.as(t, []core.Perm{acc.perm}), pattern, acc.kind, m.shop); err != nil {
				t.Errorf("%s: refused with %s: %v", pattern, acc.perm, err)
			}
			if acc.perm != core.PermView {
				if err := m.authorize(m.as(t, others(acc.perm)), pattern, acc.kind, m.shop); !errors.Is(err, core.ErrForbidden) {
					t.Errorf("%s: allowed without %s: %v", pattern, acc.perm, err)
				}
			}
			if err := m.authorize(m.as(t, core.Perms), pattern, acc.kind, m.other); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: another project's should be not found: %v", pattern, err)
			}
		default:
			if err := m.authorize(m.as(t, core.Perms, core.Features...), pattern, "", nil); !errors.Is(err, core.ErrForbidden) {
				t.Errorf("%s: allowed to a member: %v", pattern, err)
			}
		}
	}
	// Data and files routes need the service to be visible; core checks the rest.
	if err := m.authorize(m.as(t, []core.Perm{core.PermView}), "GET /api/services/{id}/data/tables", core.IDService, m.shop); err != nil {
		t.Errorf("data route refused with view: %v", err)
	}
	if err := m.authorize(m.as(t, core.Perms), "GET /api/services/{id}/files", core.IDService, m.other); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("files route of another project: %v", err)
	}
}

func TestMemberRequests(t *testing.T) {
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	do := func(tok, method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	shopSvc, otherSvc := m.shop[core.IDService], m.other[core.IDService]

	viewer := m.token(t, core.PermView)
	code, body := do(viewer, "GET", "/api/projects", "")
	var ps []store.Project
	if err := json.Unmarshal([]byte(body), &ps); code != http.StatusOK || err != nil || len(ps) != 1 || ps[0].Name != "shop" {
		t.Fatalf("projects: %d %s", code, body)
	}
	if code, body := do(viewer, "GET", "/api/projects/"+m.shop[core.IDProject], ""); code != http.StatusOK {
		t.Errorf("granted project: %d %s", code, body)
	}
	if code, body := do(viewer, "GET", "/api/projects/"+m.other[core.IDProject], ""); code != http.StatusNotFound {
		t.Errorf("another project: %d %s", code, body)
	}
	if code, body := do(viewer, "POST", "/api/projects", `{"name":"mine"}`); code != http.StatusForbidden {
		t.Errorf("create project without the feature: %d %s", code, body)
	}
	code, body = do(viewer, "GET", "/api/status", "")
	var st core.Status
	if err := json.Unmarshal([]byte(body), &st); code != http.StatusOK || err != nil || st.Docker != nil || st.DockerError != "" || st.Database != nil {
		t.Errorf("status shows a member only the version: %d %s", code, body)
	}

	// settings without env or domains: those parts of a change are refused.
	settings := m.token(t, core.PermSettings)
	for name, patch := range map[string]string{
		"env":         `{"env":{"A":"2"}}`,
		"domain":      `{"domain":"shop.example.com"}`,
		"secret file": `{"files":[{"path":"/run/a","secret":true,"content":"e3sgcHJvamVjdC5BIH19"}]}`,
	} {
		if code, body := do(settings, "PATCH", "/api/services/"+shopSvc, patch); code != http.StatusForbidden {
			t.Errorf("%s without its permission: %d %s", name, code, body)
		}
	}
	// Host access is root on the host: admins only, whatever the grants.
	all := m.token(t, core.Perms...)
	for name, patch := range map[string]string{
		"host network":  `{"hostNetwork":true}`,
		"docker socket": `{"dockerSocket":"/var/run/docker.sock"}`,
	} {
		if code, body := do(all, "PATCH", "/api/services/"+shopSvc, patch); code != http.StatusForbidden {
			t.Errorf("%s by a member: %d %s", name, code, body)
		}
	}
	// A restore can't overwrite or read another project's service.
	restore := `{"serviceId":"` + otherSvc + `","confirm":"web"}`
	if code, body := do(all, "POST", "/api/backups/"+m.shop[core.IDBackup]+"/restore", restore); code != http.StatusNotFound {
		t.Errorf("restore into another project: %d %s", code, body)
	}
	// Password manager references need the secret-providers feature.
	if code, body := do(all, "PATCH", "/api/services/"+shopSvc, `{"env":{"A":"{{ op://vault/item/field }}"}}`); code != http.StatusForbidden {
		t.Errorf("password manager reference without the feature: %d %s", code, body)
	}
	for name, patch := range map[string]string{
		"secret file": `{"files":[{"path":"/run/a","secret":true,"content":"e3sgb3A6Ly92YXVsdC9pdGVtL2ZpZWxkIH19"}]}`,
		"basic auth":  `{"middlewares":{"basicAuth":[{"name":"u","password":"{{ op://vault/item/field }}"}]}}`,
	} {
		if code, body := do(all, "PATCH", "/api/services/"+shopSvc, patch); code != http.StatusForbidden {
			t.Errorf("password manager reference in a %s without the feature: %d %s", name, code, body)
		}
	}

	// Server resources a member may see come without how they connect.
	if _, err := m.st.CreateBackupTarget(context.Background(), store.BackupTarget{Name: "s3", Kind: store.BackupTargetS3,
		Endpoint: "s3.example.com", Bucket: "bkt", AccessKey: "AKIAEXAMPLE", SecretKey: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	tok, err := m.c.CreateAPIToken(m.as(t, nil, core.FeatureStorage), core.TokenInput{Name: "storage"})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := do(tok.Token, "GET", "/api/storage", ""); code != http.StatusOK || !strings.Contains(body, `"s3"`) ||
		strings.Contains(body, "AKIAEXAMPLE") || strings.Contains(body, "s3.example.com") {
		t.Errorf("storage for a member: %d %s", code, body)
	}
}

func TestMemberTokens(t *testing.T) {
	m := newMembers(t)
	ctx := context.Background()
	admin, err := m.st.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	adminActor, err := m.c.UserActor(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	adminCtx, memberCtx := core.WithActor(ctx, adminActor), m.as(t, []core.Perm{core.PermView})
	adminTok, err := m.c.CreateAPIToken(adminCtx, core.TokenInput{Name: "admin's"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := m.c.CreateAPIToken(memberCtx, core.TokenInput{Name: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	if ts, err := m.c.ListAPITokens(memberCtx); err != nil || len(ts) != 1 || ts[0].ID != mine.ID {
		t.Fatalf("a member lists their own tokens: %+v %v", ts, err)
	}
	if ts, err := m.c.ListAPITokens(adminCtx); err != nil || len(ts) != 2 {
		t.Fatalf("an admin lists every token: %+v %v", ts, err)
	}
	if err := m.c.DeleteAPIToken(memberCtx, adminTok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a member deleting another's token: %v", err)
	}
	// An admin-scoped token of a member still has only their permissions.
	a, err := m.c.AuthenticateToken(ctx, mine.Token)
	if err != nil || a.Admin() || !a.Can(m.shop[core.IDProject], core.PermView) || a.Can(m.shop[core.IDProject], core.PermDeploy) {
		t.Fatalf("member's token: %+v %v", a, err)
	}
	if err := m.c.DeleteAPIToken(memberCtx, mine.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.c.DeleteAPIToken(adminCtx, adminTok.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictedTokens(t *testing.T) {
	m := newMembers(t)
	ctx := context.Background()
	admin, err := m.st.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	adminActor, err := m.c.UserActor(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := core.WithActor(ctx, adminActor)
	shop, other := m.shop[core.IDProject], m.other[core.IDProject]
	restricted := func(ctx context.Context, features []string, grants map[string][]string) (core.Actor, error) {
		t.Helper()
		tok, err := m.c.CreateAPIToken(ctx, core.TokenInput{Name: "agent", Restricted: true, Features: features, Grants: grants})
		if err != nil {
			return core.Actor{}, err
		}
		return m.c.AuthenticateToken(ctx, tok.Token)
	}

	// An admin's restricted token acts as a member with what it was given.
	a, err := restricted(adminCtx, []string{string(core.FeatureStorage)}, map[string][]string{shop: {"deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Admin() || !a.Can(shop, core.PermView) || !a.Can(shop, core.PermDeploy) || a.Can(shop, core.PermData) ||
		a.Can(other, core.PermView) || !a.Has(core.FeatureStorage) || a.Has(core.FeatureServers) {
		t.Fatalf("admin's restricted token: %+v", a)
	}
	if err := m.authorize(core.WithActor(ctx, a), "GET /api/users", "", nil); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("restricted token reached an admin route: %v", err)
	}

	// A member's token can't be given more than the member holds.
	memberCtx := m.as(t, []core.Perm{core.PermView, core.PermDeploy}, core.FeatureStorage)
	if _, err := restricted(memberCtx, nil, map[string][]string{shop: {"data"}}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("token beyond the member's permissions: %v", err)
	}
	if _, err := restricted(memberCtx, nil, map[string][]string{other: {"view"}}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("token on a project the member can't see: %v", err)
	}
	if _, err := restricted(memberCtx, []string{string(core.FeatureServers)}, nil); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("token with a feature the member lacks: %v", err)
	}
	if _, err := restricted(memberCtx, nil, map[string][]string{shop: {"nope"}}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("unknown permission: %v", err)
	}
	tok, err := m.c.CreateAPIToken(memberCtx, core.TokenInput{Name: "viewer", Restricted: true,
		Features: []string{string(core.FeatureStorage)}, Grants: map[string][]string{shop: {"view", "deploy"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Restricted to nothing, a token sees no project.
	a, err = restricted(memberCtx, nil, nil)
	if err != nil || a.Can(shop, core.PermView) || a.Has(core.FeatureStorage) {
		t.Fatalf("token restricted to nothing: %+v %v", a, err)
	}

	// Editing a token keeps it within its editor's access, and changes it in place.
	edit := core.TokenInput{Name: "deployer", Restricted: true, Grants: map[string][]string{shop: {"deploy"}}}
	if _, err := m.c.UpdateAPIToken(memberCtx, tok.ID, core.TokenInput{Name: "x", Restricted: true,
		Grants: map[string][]string{shop: {"data"}}}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("editing a token beyond the member's permissions: %v", err)
	}
	adminTok, err := m.c.CreateAPIToken(adminCtx, core.TokenInput{Name: "admin's"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.c.UpdateAPIToken(memberCtx, adminTok.ID, edit); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a member editing another's token: %v", err)
	}
	if u, err := m.c.UpdateAPIToken(memberCtx, tok.ID, edit); err != nil || u.Name != "deployer" || len(u.Features) != 0 {
		t.Fatalf("editing a token: %+v %v", u, err)
	}
	a, err = m.c.AuthenticateToken(ctx, tok.Token)
	if err != nil || a.Name != "deployer" || !a.Can(shop, core.PermDeploy) || a.Has(core.FeatureStorage) {
		t.Fatalf("token after its edit: %+v %v", a, err)
	}

	// Rolling a token replaces its secret and keeps its access.
	if _, err := m.c.RollAPIToken(memberCtx, adminTok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a member rolling another's token: %v", err)
	}
	rolled, err := m.c.RollAPIToken(memberCtx, tok.ID)
	if err != nil || rolled.Token == tok.Token {
		t.Fatalf("rolling a token: %+v %v", rolled, err)
	}
	if _, err := m.c.AuthenticateToken(ctx, tok.Token); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("the rolled-over secret still works: %v", err)
	}
	if a, err = m.c.AuthenticateToken(ctx, rolled.Token); err != nil || !a.Can(shop, core.PermDeploy) {
		t.Fatalf("token after its roll: %+v %v", a, err)
	}
	tok = rolled

	// The owner losing a permission or feature takes it from the token.
	m.as(t, []core.Perm{core.PermView})
	a, err = m.c.AuthenticateToken(ctx, tok.Token)
	if err != nil || !a.Can(shop, core.PermView) || a.Can(shop, core.PermDeploy) || a.Has(core.FeatureStorage) {
		t.Fatalf("token after the owner lost deploy: %+v %v", a, err)
	}
}

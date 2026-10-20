package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestValidateVolumes(t *testing.T) {
	tests := []struct {
		name    string
		vols    []store.Volume
		wantErr bool
	}{
		{"none", nil, false},
		{"two", []store.Volume{{Name: "uploads", Path: "/app/uploads"}, {Name: "cache", Path: "/var/cache/app"}}, false},
		{"uppercase name", []store.Volume{{Name: "Uploads", Path: "/data"}}, true},
		{"trailing dash", []store.Volume{{Name: "up-", Path: "/data"}}, true},
		{"relative path", []store.Volume{{Name: "data", Path: "data"}}, true},
		{"unclean path", []store.Volume{{Name: "data", Path: "/data/"}}, true},
		{"root", []store.Volume{{Name: "data", Path: "/"}}, true},
		{"probe dir", []store.Volume{{Name: "data", Path: probeDir + "/x"}}, true},
		{"colon", []store.Volume{{Name: "data", Path: "/a:b"}}, true},
		{"duplicate name", []store.Volume{{Name: "data", Path: "/a"}, {Name: "data", Path: "/b"}}, true},
		{"duplicate path", []store.Volume{{Name: "a", Path: "/data"}, {Name: "b", Path: "/data"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVolumes(tt.vols)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestDatabaseRefusesVolumes(t *testing.T) {
	svc := store.Service{Kind: store.ServiceKindRedis, Image: DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: newRedisEnv()}
	if err := validateService(svc); err != nil {
		t.Fatal(err)
	}
	withVolume, withPreBackup := svc, svc
	withVolume.Volumes = []store.Volume{{Name: "x", Path: "/x"}}
	withPreBackup.PreBackup = "redis-cli save"
	for _, s := range []store.Service{withVolume, withPreBackup} {
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v, want ErrInvalid", err)
		}
	}
}

func TestAppVolumeMounts(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	svc := store.Service{ID: "01ABC", ProjectID: "P1", Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 2,
		Volumes: []store.Volume{{Name: "uploads", Path: "/app/uploads"}}}
	mounts := containerSpec(p, svc, envSources{}, "D1", 1, route{}).HostConfig.Mounts
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v", mounts)
	}
	m := mounts[0]
	if m.Source != "kipitiny-vol-01abc-uploads" || m.Target != "/app/uploads" || m.ReadOnly {
		t.Errorf("mount = %+v", m)
	}
	if l := m.VolumeOptions.Labels; l[docker.LabelService] != "01ABC" || l[docker.LabelComponent] != volumeComponent || l[docker.LabelManaged] != "true" {
		t.Errorf("volume labels = %v", l)
	}
	if !hasData(svc) || hasData(store.Service{Kind: store.ServiceKindApp}) {
		t.Error("hasData")
	}
}

func TestBackupKind(t *testing.T) {
	app := store.Service{ID: "01APP", ProjectID: "P1", Name: "web", Kind: store.ServiceKindApp,
		Volumes: []store.Volume{{Name: "uploads", Path: "/app/uploads"}, {Name: "cache", Path: "/cache"}}}
	redis := store.Service{ID: "01RED", Name: "cache", Kind: store.ServiceKindRedis}
	pg := store.Service{ID: "01PG", Name: "db", Kind: store.ServiceKindPostgres}
	tests := []struct {
		svc   store.Service
		kind  store.BackupKind
		names []string
	}{
		{app, store.BackupKindVolume, []string{"uploads", "cache"}},
		{redis, store.BackupKindVolume, []string{"data"}},
		{pg, store.BackupKindPostgres, nil},
		{store.Service{Name: "worker", Kind: store.ServiceKindApp}, "", []string{}},
	}
	for _, tt := range tests {
		kind, err := backupKind(tt.svc)
		if kind != tt.kind || (err != nil) != (tt.kind == "") {
			t.Errorf("%s: kind = %q, %v", tt.svc.Name, kind, err)
		}
		if got := volumeNames(tt.svc); !slices.Equal(got, tt.names) {
			t.Errorf("%s: names = %v, want %v", tt.svc.Name, got, tt.names)
		}
	}

	m := helperMounts(app, []string{"cache"}, "/v", true)
	if len(m) != 1 || m[0].Source != AppVolume("01APP", "cache") || m[0].Target != "/v/cache" || !m[0].ReadOnly ||
		m[0].VolumeOptions.Labels[docker.LabelService] != "01APP" {
		t.Errorf("app helper mounts = %+v", m)
	}
	ro := app
	ro.Volumes = slices.Clone(app.Volumes)
	for i := range ro.Volumes {
		ro.Volumes[i].ReadOnly = true
	}
	if m := helperMounts(ro, []string{"cache"}, "/v", false); !m[0].ReadOnly {
		t.Errorf("a read-only volume stays read-only in helpers: %+v", m)
	}
	if _, _, _, err := writableEntry(ro, "cache/a"); !errors.Is(err, ErrInvalid) {
		t.Errorf("writing a read-only volume: %v", err)
	}
	if _, _, _, err := writableEntry(app, "cache/a"); err != nil {
		t.Errorf("writing a volume: %v", err)
	}
	m = helperMounts(redis, []string{"data"}, "/v", false)
	if len(m) != 1 || m[0].Source != RedisVolume("01RED") || m[0].Target != "/v/data" || m[0].ReadOnly {
		t.Errorf("redis helper mounts = %+v", m)
	}
}

// A member with restore doesn't overwrite a volume an admin attached
// read-only; an admin does.
func TestRestoreReadOnlyHostVolume(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, ServerID: store.LocalServerID, Image: "busybox",
		Volumes: []store.Volume{{Name: "shared", Path: "/data", ReadOnly: true, External: "other-data"}}})
	if err != nil {
		t.Fatal(err)
	}
	tg, err := c.store.CreateBackupTarget(ctx, store.BackupTarget{Name: "t", Kind: "local"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.store.CreateBackup(ctx, store.Backup{Kind: store.BackupKindVolume, ServiceID: svc.ID, ProjectID: p.ID, TargetID: tg.ID, Status: store.OpRunning, Volumes: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	b.Status = store.OpSucceeded
	if err := c.store.FinishBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	member := WithActor(ctx, Actor{Kind: "user", Name: "alice", Role: store.RoleMember,
		Grants: map[string][]string{p.ID: {string(PermView), string(PermRestore)}}})
	if _, err := c.RestoreBackup(member, b.ID, "", "web"); !errors.Is(err, ErrForbidden) {
		t.Errorf("member restoring a read-only external volume: %v", err)
	}
	// The wrong name stops the admin before any Docker work.
	admin := WithActor(ctx, Actor{Kind: "user", Name: "root", Role: store.RoleAdmin})
	if _, err := c.RestoreBackup(admin, b.ID, "", "wrong"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("admin restoring a read-only external volume: %v", err)
	}
}

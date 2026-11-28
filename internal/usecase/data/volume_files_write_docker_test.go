package data

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestVolumeFilesDockerWrite(t *testing.T) {
	c, readCtx, svc := filesTestService(t, domain.Service{Name: "mc", Kind: domain.ServiceKindApp,
		Volumes: []domain.Volume{{Name: "data", Path: "/data"}, {Name: "logs", Path: "/logs"}}}, "data", `
cd /seed
mkdir mods
chown 1000:1000 mods
printf 'secret' > ops.json
chown 1000:1000 ops.json
chmod 600 ops.json
ln -s /etc etc-link
ln -s .. up`)
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})

	if err := c.MakeVolumeDir(readCtx, svc.ID, "data/x"); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("mkdir with only the files permission: %v", err)
	}
	if err := c.MakeVolumeDir(ctx, svc.ID, "data/mods/new/deep"); err != nil {
		t.Fatal(err)
	}
	if l, err := c.ListVolumeFiles(ctx, svc.ID, "data/mods/new"); err != nil || len(l.Entries) != 1 || l.Entries[0].UID != 1000 {
		t.Errorf("new folders belong to the parent's owner: %+v %v", l, err)
	}
	for p, want := range map[string]error{
		"data/mods/new":    domain.ErrConflict,
		"data/etc-link/x":  ErrInvalid,
		"data/up/x":        ErrInvalid,
		"data":             ErrInvalid,
		"data/ops.json/x":  ErrInvalid,
		"nothere/x":        domain.ErrNotFound,
		"data/../../etc/x": ErrInvalid,
	} {
		if err := c.MakeVolumeDir(ctx, svc.ID, p); !errors.Is(err, want) {
			t.Errorf("mkdir %s: %v, want %v", p, err, want)
		}
	}

	e, err := c.WriteVolumeFile(ctx, svc.ID, "data/mods/sub/b.jar", strings.NewReader("jar!"), WriteOptions{})
	if err != nil || e.Name != "b.jar" || e.Size != 4 || e.UID != 1000 || e.Mode != "0644" {
		t.Fatalf("upload: %+v %v", e, err)
	}
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/mods/sub/b.jar", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("upload over a file: %v", err)
	}
	f, err := c.ReadVolumeFile(ctx, svc.ID, "data/ops.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/ops.json", strings.NewReader("[]"), WriteOptions{Overwrite: true, Modified: time.Unix(1, 0)}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("save over a changed file: %v", err)
	}
	if e, err := c.WriteVolumeFile(ctx, svc.ID, "data/ops.json", strings.NewReader("[]"), WriteOptions{Overwrite: true, Modified: f.Modified}); err != nil || e.Mode != "0600" || e.UID != 1000 || e.Size != 2 {
		t.Errorf("a replaced file keeps owner and mode: %+v %v", e, err)
	}
	for p, want := range map[string]error{
		"data/etc-link/passwd": ErrInvalid,
		"data/up/x":            ErrInvalid,
		"data/mods":            ErrInvalid, // a folder
	} {
		if _, err := c.WriteVolumeFile(ctx, svc.ID, p, strings.NewReader("x"), WriteOptions{Overwrite: true}); !errors.Is(err, want) {
			t.Errorf("write %s: %v, want %v", p, err, want)
		}
	}
	cut := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("client went away")))
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/cut.bin", cut, WriteOptions{}); err == nil {
		t.Error("cut upload: no error")
	}
	if l, err := c.ListVolumeFiles(ctx, svc.ID, "data"); err != nil || slices.ContainsFunc(l.Entries, func(e VolumeEntry) bool {
		return e.Name == "cut.bin" || strings.HasPrefix(e.Name, ".kipitiny-upload-")
	}) {
		t.Errorf("a cut upload left files: %+v %v", l, err)
	}

	if err := c.CopyVolumePath(ctx, svc.ID, "data/mods", "logs/mods-copy"); err != nil {
		t.Fatal(err)
	}
	if f, err := c.ReadVolumeFile(ctx, svc.ID, "logs/mods-copy/sub/b.jar"); err != nil || f.Content != "jar!" || f.UID != 1000 {
		t.Errorf("copy across volumes: %+v %v", f, err)
	}
	if err := c.MoveVolumePath(ctx, svc.ID, "data/mods/sub/b.jar", "data/b.jar"); err != nil {
		t.Fatal(err)
	}
	if err := c.MoveVolumePath(ctx, svc.ID, "data/etc-link", "data/mods/etc"); err != nil {
		t.Fatal(err)
	}
	if e, err := c.ListVolumeFiles(ctx, svc.ID, "data/mods"); err != nil || !slices.ContainsFunc(e.Entries, func(e VolumeEntry) bool { return e.Name == "etc" && e.Target == "/etc" }) {
		t.Errorf("a moved symlink stays a link: %+v %v", e, err)
	}
	for _, tc := range []struct {
		from, to string
		want     error
	}{
		{"data/b.jar", "data/ops.json", domain.ErrConflict},
		{"data/mods", "data/mods/new/inside", ErrInvalid},
		{"data/missing", "data/x", domain.ErrNotFound},
		{"data/b.jar", "data/nofolder/b.jar", domain.ErrNotFound},
		{"data/b.jar", "data/up/b.jar", ErrInvalid},
		{"data", "logs/data", ErrInvalid},
	} {
		if err := c.MoveVolumePath(ctx, svc.ID, tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("move %s -> %s: %v, want %v", tc.from, tc.to, err, tc.want)
		}
	}

	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/b.jar", "data/missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete with a missing path: %v", err)
	}
	if _, err := c.StatVolumePath(ctx, svc.ID, "data/b.jar"); err != nil {
		t.Errorf("a failed delete removed something: %v", err)
	}
	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/b.jar", "data/mods/etc", "logs/mods-copy", "data/up"}); err != nil {
		t.Fatal(err)
	}
	h := c.files.helpers[svc.ID]
	if err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, h.id, domain.ExecOptions{Cmd: []string{"test", "-f", "/etc/passwd"}}); err != nil {
		t.Errorf("deleting a symlink followed it: %v", err)
	}
	l, err := c.ListVolumeFiles(ctx, svc.ID, "data")
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if err != nil || !slices.Equal(names, []string{"mods", "ops.json"}) {
		t.Errorf("after delete: %q %v", names, err)
	}

	audit, err := c.store.ListAudit(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, a := range audit {
		if a.Target == svc.ID+":data/mods/sub/b.jar" || a.Target == svc.ID+":data/b.jar, data/mods/etc, logs/mods-copy, data/up" {
			actions = append(actions, a.Action)
		}
	}
	if !slices.Contains(actions, "volume write") || !slices.Contains(actions, "volume delete") {
		t.Errorf("audit: %q", actions)
	}
}

func TestVolumeFilesDockerDatabaseWrite(t *testing.T) {
	c, _, svc := filesTestService(t, domain.Service{Name: "db", Kind: domain.ServiceKindRedis}, "", `printf x > /seed/dump.rdb`)
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/dump.rdb"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("delete in a database: %v", err)
	}
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/x", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("write in a database: %v", err)
	}
}

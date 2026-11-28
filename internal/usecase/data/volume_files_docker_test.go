package data

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
)

// filesTestService creates svc and fills its volume (named vol) with a shell
// script, run against the local Docker daemon.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/usecase/data/ -run VolumeFilesDocker
func filesTestService(t *testing.T, svc domain.Service, vol, seed string) (*Data, context.Context, domain.Service) {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestData(t, config.Config{DataDir: t.TempDir()}, dk)
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "filestest", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc.ProjectID, svc.Replicas, svc.Env = p.ID, 1, map[string]string{}
	if svc, err = c.store.CreateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	source, all := DataVolume(svc), []string{DataVolume(svc)}
	if source == "" {
		source, all = AppVolume(svc.ID, vol), nil
		for _, v := range svc.Volumes {
			all = append(all, AppVolume(svc.ID, v.Name))
		}
	}
	t.Cleanup(func() {
		c.CloseFilesHelpers()
		for _, v := range all {
			_ = dk.RemoveVolume(context.Background(), v)
		}
	})
	if err := dk.EnsureImage(ctx, volumeHelperImage, "", ""); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := dk.RunAttached(ctx, domain.ContainerSpec{
		Image: volumeHelperImage, Cmd: []string{"sh", "-ec", seed},
		NetworkMode: "none", Mounts: []domain.Mount{{Type: domain.MountVolume, Source: source, Target: "/seed"}},
	}, nil, &out)
	if err != nil || code != 0 {
		t.Fatalf("seed: %d %v %s", code, err, out.String())
	}
	return c, identity.WithActor(ctx, reader(svc, "files")), svc
}

func TestVolumeFilesDockerApp(t *testing.T) {
	c, ctx, svc := filesTestService(t, domain.Service{Name: "mc", Kind: domain.ServiceKindApp,
		Volumes: []domain.Volume{{Name: "data", Path: "/data"}}}, "data", `
cd /seed
mkdir -p mods/deep .hidden
printf 'motd=hi\n' > server.properties
printf 'jar' > mods/a.jar
printf '\0\1' > bin.dat
printf x > "new
line"
printf x > -dash
chown 1000:1000 mods/a.jar
ln -s /etc etc-link
ln -s .. up
ln -s mods/a.jar ok-link`)

	root, err := c.ListVolumeFiles(ctx, svc.ID, "")
	if err != nil || len(root.Entries) != 2 || root.Entries[0].Name != "data" || root.Entries[0].MountPath != "/data" || root.Entries[1].Name != "@container" || root.ReadOnly {
		t.Fatalf("root: %+v %v", root, err)
	}
	l, err := c.ListVolumeFiles(ctx, svc.ID, "data")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if want := []string{".hidden", "mods", "-dash", "bin.dat", "etc-link", "new\nline", "ok-link", "server.properties", "up"}; !slices.Equal(names, want) {
		t.Errorf("listing: %q, want %q", names, want)
	}
	if i := slices.Index(names, "etc-link"); i < 0 || l.Entries[i].Type != "link" || l.Entries[i].Target != "/etc" {
		t.Errorf("symlink: %+v", l.Entries)
	}

	m, err := c.ListVolumeFiles(ctx, svc.ID, "data/mods")
	if err != nil || len(m.Entries) != 2 || m.Entries[1].Name != "a.jar" || m.Entries[1].UID != 1000 || m.Entries[1].Size != 3 {
		t.Errorf("mods: %+v %v", m, err)
	}
	if l, err := c.ListVolumeFiles(ctx, svc.ID, "data/mods/deep"); err != nil || len(l.Entries) != 0 || l.Entries == nil {
		t.Errorf("empty folder: %+v %v", l, err)
	}

	for p, want := range map[string]error{
		"data/missing":           domain.ErrNotFound,
		"data/server.properties": ErrInvalid, // not a folder
		"data/etc-link":          ErrInvalid, // outside
		"data/up":                ErrInvalid, // outside
	} {
		if _, err := c.ListVolumeFiles(ctx, svc.ID, p); !errors.Is(err, want) {
			t.Errorf("list %s: %v, want %v", p, err, want)
		}
	}

	// Contents need the files permission, like listing.
	viewer := identity.WithActor(ctx, reader(svc))
	if _, err := c.ReadVolumeFile(viewer, svc.ID, "data/server.properties"); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("read as a viewer: %v", err)
	}
	if err := c.DownloadVolumeFile(viewer, svc.ID, "data/server.properties", io.Discard); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("download as a viewer: %v", err)
	}
	if err := c.ArchiveVolumeFiles(viewer, svc.ID, "data", nil, io.Discard); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("archive as a viewer: %v", err)
	}
	ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})

	f, err := c.ReadVolumeFile(ctx, svc.ID, "data/server.properties")
	if err != nil || f.Content != "motd=hi\n" || f.Type != "file" || f.Path != "data/server.properties" {
		t.Errorf("read: %+v %v", f, err)
	}
	if f, err := c.ReadVolumeFile(ctx, svc.ID, "data/ok-link"); err != nil || f.Content != "jar" {
		t.Errorf("read through a symlink: %+v %v", f, err)
	}
	for p, want := range map[string]error{
		"data/bin.dat":  ErrInvalid, // binary
		"data/mods":     ErrInvalid, // folder
		"data/etc-link": ErrInvalid,
		"data/nope":     domain.ErrNotFound,
	} {
		if _, err := c.ReadVolumeFile(ctx, svc.ID, p); !errors.Is(err, want) {
			t.Errorf("read %s: %v, want %v", p, err, want)
		}
	}

	if e, err := c.StatVolumePath(ctx, svc.ID, "data/-dash"); err != nil || e.Type != "file" || e.Name != "-dash" {
		t.Errorf("stat: %+v %v", e, err)
	}
	if e, err := c.StatVolumePath(ctx, svc.ID, "data"); err != nil || e.Type != "volume" {
		t.Errorf("stat volume: %+v %v", e, err)
	}

	var raw bytes.Buffer
	if err := c.DownloadVolumeFile(ctx, svc.ID, "data/bin.dat", &raw); err != nil || raw.String() != "\x00\x01" {
		t.Errorf("download: %q %v", raw.String(), err)
	}
	if err := c.DownloadVolumeFile(ctx, svc.ID, "data/etc-link/passwd", io.Discard); !errors.Is(err, ErrInvalid) {
		t.Errorf("download outside: %v", err)
	}

	if got := archiveNames(ctx, t, c, svc.ID, "data/mods", nil); !slices.Equal(got, []string{"mods/", "mods/a.jar", "mods/deep/"}) {
		t.Errorf("folder archive: %q", got)
	}
	if got := archiveNames(ctx, t, c, svc.ID, "data", []string{"-dash", "etc-link"}); !slices.Equal(got, []string{"-dash", "etc-link"}) {
		t.Errorf("selection archive: %q", got)
	}
	for _, names := range [][]string{{"../x"}, {"missing"}, {""}} {
		if err := c.ArchiveVolumeFiles(ctx, svc.ID, "data", names, io.Discard); err == nil {
			t.Errorf("archive %q: no error", names)
		}
	}

	// One helper serves every call, and comes back if removed by hand.
	h := c.files.helpers[svc.ID]
	if h == nil || h.id == "" || h.busy != 0 || h.idle == nil {
		t.Fatalf("helper: %+v", h)
	}
	if err := c.mgr.DockerFor(svc.ServerID).RemoveContainerAndVolumes(ctx, h.id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListVolumeFiles(ctx, svc.ID, "data"); err != nil {
		t.Errorf("after the helper went away: %v", err)
	}
	c.CloseFilesHelper(svc.ID)
	if c.files.helpers[svc.ID] != nil {
		t.Error("helper kept after close")
	}

	if _, err := c.ListVolumeFiles(identity.WithActor(context.Background(), identity.Actor{Kind: "token"}), svc.ID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no access: %v", err)
	}
}

func TestVolumeFilesDockerDatabase(t *testing.T) {
	c, ctx, svc := filesTestService(t, domain.Service{Name: "db", Kind: domain.ServiceKindPostgres}, "", `printf 1 > /seed/PG_VERSION`)
	l, err := c.ListVolumeFiles(ctx, svc.ID, "data")
	if err != nil || !l.ReadOnly || len(l.Entries) != 1 || l.Entries[0].Name != "PG_VERSION" {
		t.Fatalf("listing: %+v %v", l, err)
	}
	h := c.files.helpers[svc.ID]
	err = c.mgr.DockerFor(svc.ServerID).Exec(ctx, h.id, domain.ExecOptions{Cmd: []string{"touch", "/v/data/x"}})
	if err == nil || !strings.Contains(err.Error(), "Read-only") {
		t.Errorf("the helper can write a database volume: %v", err)
	}
}

func archiveNames(ctx context.Context, t *testing.T, c *Data, id, dir string, names []string) []string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.ArchiveVolumeFiles(ctx, id, dir, names, &buf); err != nil {
		t.Fatalf("archive %s %q: %v", dir, names, err)
	}
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var got []string
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hd.Name)
	}
	slices.Sort(got)
	return got
}

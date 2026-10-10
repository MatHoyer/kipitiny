package core

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
	"testing/iotest"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// filesTestService creates svc and fills its volume (named vol) with a shell
// script, run against the local Docker daemon.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run VolumeFilesDocker
func filesTestService(t *testing.T, svc store.Service, vol, seed string) (*Core, context.Context, store.Service) {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)
	p, err := c.store.CreateProject(ctx, store.Project{Name: "filestest", ServerID: store.LocalServerID})
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
		c.closeFilesHelpers()
		for _, v := range all {
			_ = dk.RemoveVolume(context.Background(), v)
		}
	})
	if err := dk.EnsureImage(ctx, volumeHelperImage, ""); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := dk.RunAttached(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: volumeHelperImage, Cmd: []string{"sh", "-ec", seed}},
		HostConfig: &container.HostConfig{NetworkMode: "none", Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: source, Target: "/seed"}}},
	}, nil, &out)
	if err != nil || code != 0 {
		t.Fatalf("seed: %d %v %s", code, err, out.String())
	}
	return c, WithActor(ctx, Actor{Kind: "token", Name: "test", Scope: store.ScopeRead}), svc
}

func TestVolumeFilesDockerApp(t *testing.T) {
	c, ctx, svc := filesTestService(t, store.Service{Name: "mc", Kind: store.ServiceKindApp,
		Volumes: []store.Volume{{Name: "data", Path: "/data"}}}, "data", `
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
		"data/missing":           store.ErrNotFound,
		"data/server.properties": ErrInvalid, // not a folder
		"data/etc-link":          ErrInvalid, // outside
		"data/up":                ErrInvalid, // outside
	} {
		if _, err := c.ListVolumeFiles(ctx, svc.ID, p); !errors.Is(err, want) {
			t.Errorf("list %s: %v, want %v", p, err, want)
		}
	}

	// Listing needs read; contents, like backups, admin.
	if _, err := c.ReadVolumeFile(ctx, svc.ID, "data/server.properties"); !errors.Is(err, ErrForbidden) {
		t.Errorf("read with a read token: %v", err)
	}
	if err := c.DownloadVolumeFile(ctx, svc.ID, "data/server.properties", io.Discard); !errors.Is(err, ErrForbidden) {
		t.Errorf("download with a read token: %v", err)
	}
	if err := c.ArchiveVolumeFiles(ctx, svc.ID, "data", nil, io.Discard); !errors.Is(err, ErrForbidden) {
		t.Errorf("archive with a read token: %v", err)
	}
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "admin", Scope: store.ScopeAdmin})

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
		"data/nope":     store.ErrNotFound,
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

	if got := archiveNames(t, c, ctx, svc.ID, "data/mods", nil); !slices.Equal(got, []string{"mods/", "mods/a.jar", "mods/deep/"}) {
		t.Errorf("folder archive: %q", got)
	}
	if got := archiveNames(t, c, ctx, svc.ID, "data", []string{"-dash", "etc-link"}); !slices.Equal(got, []string{"-dash", "etc-link"}) {
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
	if err := c.dockerFor(svc.ServerID).RemoveContainerAndVolumes(ctx, h.id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListVolumeFiles(ctx, svc.ID, "data"); err != nil {
		t.Errorf("after the helper went away: %v", err)
	}
	c.closeFilesHelper(svc.ID)
	if c.files.helpers[svc.ID] != nil {
		t.Error("helper kept after close")
	}

	if _, err := c.ListVolumeFiles(WithActor(context.Background(), Actor{Kind: "token", Scope: ""}), svc.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("no scope: %v", err)
	}
}

func TestVolumeFilesDockerDatabase(t *testing.T) {
	c, ctx, svc := filesTestService(t, store.Service{Name: "db", Kind: store.ServiceKindPostgres}, "", `printf 1 > /seed/PG_VERSION`)
	l, err := c.ListVolumeFiles(ctx, svc.ID, "data")
	if err != nil || !l.ReadOnly || len(l.Entries) != 1 || l.Entries[0].Name != "PG_VERSION" {
		t.Fatalf("listing: %+v %v", l, err)
	}
	h := c.files.helpers[svc.ID]
	err = c.dockerFor(svc.ServerID).Exec(ctx, h.id, docker.ExecOptions{Cmd: []string{"touch", "/v/data/x"}})
	if err == nil || !strings.Contains(err.Error(), "Read-only") {
		t.Errorf("the helper can write a database volume: %v", err)
	}
}

func archiveNames(t *testing.T, c *Core, ctx context.Context, id, dir string, names []string) []string {
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

func TestVolumeFilesDockerWrite(t *testing.T) {
	c, readCtx, svc := filesTestService(t, store.Service{Name: "mc", Kind: store.ServiceKindApp,
		Volumes: []store.Volume{{Name: "data", Path: "/data"}, {Name: "logs", Path: "/logs"}}}, "data", `
cd /seed
mkdir mods
chown 1000:1000 mods
printf 'secret' > ops.json
chown 1000:1000 ops.json
chmod 600 ops.json
ln -s /etc etc-link
ln -s .. up`)
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "admin", Scope: store.ScopeAdmin})

	if err := c.MakeVolumeDir(readCtx, svc.ID, "data/x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("mkdir with a read token: %v", err)
	}
	if err := c.MakeVolumeDir(ctx, svc.ID, "data/mods/new/deep"); err != nil {
		t.Fatal(err)
	}
	if l, err := c.ListVolumeFiles(ctx, svc.ID, "data/mods/new"); err != nil || len(l.Entries) != 1 || l.Entries[0].UID != 1000 {
		t.Errorf("new folders belong to the parent's owner: %+v %v", l, err)
	}
	for p, want := range map[string]error{
		"data/mods/new":    store.ErrConflict,
		"data/etc-link/x":  ErrInvalid,
		"data/up/x":        ErrInvalid,
		"data":             ErrInvalid,
		"data/ops.json/x":  ErrInvalid,
		"nothere/x":        store.ErrNotFound,
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
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/mods/sub/b.jar", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("upload over a file: %v", err)
	}
	f, err := c.ReadVolumeFile(ctx, svc.ID, "data/ops.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/ops.json", strings.NewReader("[]"), WriteOptions{Overwrite: true, Modified: time.Unix(1, 0)}); !errors.Is(err, store.ErrConflict) {
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
		{"data/b.jar", "data/ops.json", store.ErrConflict},
		{"data/mods", "data/mods/new/inside", ErrInvalid},
		{"data/missing", "data/x", store.ErrNotFound},
		{"data/b.jar", "data/nofolder/b.jar", store.ErrNotFound},
		{"data/b.jar", "data/up/b.jar", ErrInvalid},
		{"data", "logs/data", ErrInvalid},
	} {
		if err := c.MoveVolumePath(ctx, svc.ID, tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("move %s -> %s: %v, want %v", tc.from, tc.to, err, tc.want)
		}
	}

	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/b.jar", "data/missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete with a missing path: %v", err)
	}
	if _, err := c.StatVolumePath(ctx, svc.ID, "data/b.jar"); err != nil {
		t.Errorf("a failed delete removed something: %v", err)
	}
	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/b.jar", "data/mods/etc", "logs/mods-copy", "data/up"}); err != nil {
		t.Fatal(err)
	}
	h := c.files.helpers[svc.ID]
	if err := c.dockerFor(svc.ServerID).Exec(ctx, h.id, docker.ExecOptions{Cmd: []string{"test", "-f", "/etc/passwd"}}); err != nil {
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
	c, _, svc := filesTestService(t, store.Service{Name: "db", Kind: store.ServiceKindRedis}, "", `printf x > /seed/dump.rdb`)
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "admin", Scope: store.ScopeAdmin})
	if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"data/dump.rdb"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("delete in a database: %v", err)
	}
	if _, err := c.WriteVolumeFile(ctx, svc.ID, "data/x", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("write in a database: %v", err)
	}
}

// containerFilesTest runs image as svc's only replica (not kipitiny.managed:
// a manager on this daemon would remove it as an orphan).
func containerFilesTest(t *testing.T, kind store.ServiceKind, image string, cmd ...string) (*Core, context.Context, store.Service, string) {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "ctfiles", ServerID: store.LocalServerID})
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "app", Kind: kind, Replicas: 1, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{
		Name: "kipitiny-test-" + strings.ToLower(svc.ID),
		Config: &container.Config{Image: image, Cmd: cmd, User: "1000",
			Labels: map[string]string{docker.LabelProject: p.ID, docker.LabelService: svc.ID, docker.LabelReplica: "1"}},
		HostConfig: &container.HostConfig{NetworkMode: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainerAndVolumes(context.Background(), id) })
	return c, WithActor(ctx, Actor{Kind: "user", Name: "admin", Scope: store.ScopeAdmin}), svc, id
}

func TestVolumeFilesDockerContainer(t *testing.T) {
	for _, image := range []string{"alpine:3.22", "debian:bookworm-slim"} {
		t.Run(image, func(t *testing.T) {
			c, ctx, svc, id := containerFilesTest(t, store.ServiceKindApp, image, "sleep", "infinity")
			root, err := c.ListVolumeFiles(ctx, svc.ID, "")
			if err != nil || len(root.Entries) != 1 || root.Entries[0].Name != "@container" || root.Entries[0].Type != "container" {
				t.Fatalf("root: %+v %v", root, err)
			}
			l, err := c.ListVolumeFiles(ctx, svc.ID, "@container")
			if err != nil || l.Path != "@container" || !slices.ContainsFunc(l.Entries, func(e VolumeEntry) bool { return e.Name == "etc" && e.Type == "dir" }) {
				t.Fatalf("listing /: %+v %v", l, err)
			}
			// Runs as root whatever the image's user (1000 here): /root is 0700.
			if _, err := c.ListVolumeFiles(ctx, svc.ID, "@container/root"); err != nil {
				t.Errorf("list /root: %v", err)
			}
			f, err := c.ReadVolumeFile(ctx, svc.ID, "@container/etc/hostname")
			if err != nil || f.Path != "@container/etc/hostname" || f.Content == "" {
				t.Errorf("read: %+v %v", f, err)
			}
			if e, err := c.StatVolumePath(ctx, svc.ID, "@container"); err != nil || e.Type != "container" {
				t.Errorf("stat /: %+v %v", e, err)
			}

			// Writes go to that replica, created files owned like their folder.
			if err := c.MakeVolumeDir(ctx, svc.ID, "@container/srv/conf"); err != nil {
				t.Fatal(err)
			}
			if _, err := c.WriteVolumeFile(ctx, svc.ID, "@container/srv/conf/app.ini", strings.NewReader("a=1\n"), WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.WriteVolumeFile(ctx, svc.ID, "@container/top.txt", strings.NewReader("top"), WriteOptions{}); err != nil {
				t.Errorf("write at /: %v", err)
			}
			if err := c.MoveVolumePath(ctx, svc.ID, "@container/top.txt", "@container/srv/top.txt"); err != nil {
				t.Errorf("move: %v", err)
			}
			var out strings.Builder
			if err := c.dockerFor(svc.ServerID).Exec(ctx, id, docker.ExecOptions{Cmd: []string{"cat", "/srv/conf/app.ini", "/srv/top.txt"}, Stdout: &out}); err != nil || out.String() != "a=1\ntop" {
				t.Errorf("in the container: %q %v", out.String(), err)
			}
			if got := archiveNames(t, c, ctx, svc.ID, "@container/srv", nil); !slices.Contains(got, "srv/conf/app.ini") {
				t.Errorf("archive: %q", got)
			}
			if err := c.ArchiveVolumeFiles(ctx, svc.ID, "@container", nil, io.Discard); !errors.Is(err, ErrInvalid) {
				t.Errorf("archive of /: %v", err)
			}
			if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"@container/srv/conf"}); err != nil {
				t.Errorf("delete: %v", err)
			}
			for p, want := range map[string]error{"@container/nope": store.ErrNotFound, "@container": ErrInvalid} {
				if err := c.DeleteVolumePaths(ctx, svc.ID, []string{p}); !errors.Is(err, want) {
					t.Errorf("delete %s: %v, want %v", p, err, want)
				}
			}
			if _, err := c.ListVolumeFiles(ctx, svc.ID, "@"+id[:12]+"/etc"); err != nil {
				t.Errorf("by container ID: %v", err)
			}
			if _, err := c.ListVolumeFiles(ctx, svc.ID, "@0123456789ab/etc"); !errors.Is(err, ErrInvalid) {
				t.Errorf("unknown container: %v", err)
			}
		})
	}
}

func TestVolumeFilesDockerContainerNoShell(t *testing.T) {
	c, ctx, svc, _ := containerFilesTest(t, store.ServiceKindApp, "registry.k8s.io/pause:3.10")
	_, err := c.ListVolumeFiles(ctx, svc.ID, "@container")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "lacks a shell") {
		t.Errorf("no shell: %v", err)
	}
}

func TestVolumeFilesDockerContainerDatabase(t *testing.T) {
	c, ctx, svc, _ := containerFilesTest(t, store.ServiceKindRedis, "alpine:3.22", "sleep", "infinity")
	if _, err := c.ListVolumeFiles(ctx, svc.ID, "@container/etc"); err != nil {
		t.Errorf("list: %v", err)
	}
	if err := c.MakeVolumeDir(ctx, svc.ID, "@container/x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("write in a database's container: %v", err)
	}
}

package data

import (
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

// containerFilesTest runs image as svc's only replica (not kipitiny.managed:
// a manager on this daemon would remove it as an orphan).
func containerFilesTest(t *testing.T, kind domain.ServiceKind, image string, cmd ...string) (*Data, context.Context, domain.Service, string) {
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
	p, _ := c.store.CreateProject(ctx, domain.Project{Name: "ctfiles", ServerID: domain.LocalServerID})
	svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "app", Kind: kind, Replicas: 1, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	// Pulled first: a fresh daemon (CI's) has no image yet.
	if err := dk.EnsureImage(ctx, image, "", ""); err != nil {
		t.Fatal(err)
	}
	id, err := dk.Run(ctx, domain.ContainerSpec{
		Name:  "kipitiny-test-" + strings.ToLower(svc.ID),
		Image: image, Cmd: cmd, User: "1000",
		Labels:      map[string]string{domain.LabelProject: p.ID, domain.LabelService: svc.ID, domain.LabelReplica: "1"},
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainerAndVolumes(context.Background(), id) })
	return c, identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin}), svc, id
}

func TestVolumeFilesDockerContainer(t *testing.T) {
	for _, image := range []string{"alpine:3.22", "debian:bookworm-slim"} {
		t.Run(image, func(t *testing.T) {
			c, ctx, svc, id := containerFilesTest(t, domain.ServiceKindApp, image, "sleep", "infinity")
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
			if err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, id, domain.ExecOptions{Cmd: []string{"cat", "/srv/conf/app.ini", "/srv/top.txt"}, Stdout: &out}); err != nil || out.String() != "a=1\ntop" {
				t.Errorf("in the container: %q %v", out.String(), err)
			}
			if got := archiveNames(ctx, t, c, svc.ID, "@container/srv", nil); !slices.Contains(got, "srv/conf/app.ini") {
				t.Errorf("archive: %q", got)
			}
			if err := c.ArchiveVolumeFiles(ctx, svc.ID, "@container", nil, io.Discard); !errors.Is(err, ErrInvalid) {
				t.Errorf("archive of /: %v", err)
			}
			if err := c.DeleteVolumePaths(ctx, svc.ID, []string{"@container/srv/conf"}); err != nil {
				t.Errorf("delete: %v", err)
			}
			for p, want := range map[string]error{"@container/nope": domain.ErrNotFound, "@container": ErrInvalid} {
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
	c, ctx, svc, _ := containerFilesTest(t, domain.ServiceKindApp, "registry.k8s.io/pause:3.10")
	_, err := c.ListVolumeFiles(ctx, svc.ID, "@container")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "lacks a shell") {
		t.Errorf("no shell: %v", err)
	}
}

func TestVolumeFilesDockerContainerDatabase(t *testing.T) {
	c, ctx, svc, _ := containerFilesTest(t, domain.ServiceKindRedis, "alpine:3.22", "sleep", "infinity")
	if _, err := c.ListVolumeFiles(ctx, svc.ID, "@container/etc"); err != nil {
		t.Errorf("list: %v", err)
	}
	if err := c.MakeVolumeDir(ctx, svc.ID, "@container/x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("write in a database's container: %v", err)
	}
}

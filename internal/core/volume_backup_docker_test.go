package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestVolumeBackupDocker backs up, verifies and restores an app volume
// against the local Docker daemon. It only touches what it creates.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run VolumeBackupDocker
func TestVolumeBackupDocker(t *testing.T) {
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
	if _, err := c.EncryptBackupTarget(ctx, store.LocalTargetID); err != nil {
		t.Fatal(err)
	}

	p, err := c.store.CreateProject(ctx, store.Project{Name: "voltest", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "busybox:stable",
		Replicas: 1, Volumes: []store.Volume{{Name: "uploads", Path: "/uploads"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, "D1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeServiceVolumes(context.Background(), dk, svc) })

	// A running replica writing to the volume, stopped during the restore.
	replica, err := dk.Run(ctx, client.ContainerCreateOptions{
		Name: "kipitiny-test-" + strings.ToLower(svc.ID),
		Config: &container.Config{
			Image: "busybox:stable",
			Cmd:   []string{"sh", "-c", "[ -f /uploads/a.txt ] || { echo v1 > /uploads/a.txt && mkdir /uploads/.d && chown 1000:1000 /uploads/a.txt; }; sleep 3600"},
			// Not kipitiny.managed: a manager running on this daemon would
			// remove it as an orphan.
			Labels: map[string]string{docker.LabelProject: p.ID, docker.LabelService: svc.ID, docker.LabelDeploy: "D1"},
		},
		HostConfig: &container.HostConfig{Mounts: []mount.Mount{appVolumeMount(svc, svc.Volumes[0])}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainerAndVolumes(context.Background(), replica) })
	time.Sleep(time.Second)
	exec := func(cmd string) string {
		t.Helper()
		var out strings.Builder
		if err := dk.Exec(ctx, replica, docker.ExecOptions{Cmd: []string{"sh", "-c", cmd}, Stdout: &out}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return strings.TrimSpace(out.String())
	}

	svc.PreBackup = "echo flushed > /uploads/flushed"
	if svc, err = c.store.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	b, err := c.BackupService(ctx, svc.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.Status != store.OpRunning })
	if b.Status != store.OpSucceeded || b.Kind != store.BackupKindVolume || !b.Encrypted || b.SizeBytes == 0 {
		t.Fatalf("backup = %+v", b)
	}

	if _, err := c.VerifyBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.VerifyStatus != store.OpRunning })
	if b.VerifyStatus != store.OpSucceeded || b.VerifyDetails.Files < 3 {
		t.Fatalf("verification = %+v", b.Verification)
	}

	exec("echo v2 > /uploads/a.txt && echo junk > /uploads/new.txt")
	if _, err := c.RestoreBackup(ctx, b.ID, "", "nope"); err == nil {
		t.Fatal("restore without confirmation")
	}
	r, err := c.RestoreBackup(ctx, b.ID, "", "web")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		rs, err := c.ListRestores(ctx, svc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rs[0].ID == r.ID && rs[0].Status != store.OpRunning {
			if rs[0].Status != store.OpSucceeded {
				t.Fatalf("restore = %+v", rs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restore did not finish")
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The replica was stopped for the swap and started again.
	if got := exec("cat /uploads/a.txt; stat -c %u /uploads/a.txt; ls -a /uploads | tr '\\n' ' '"); got != "v1\n1000\n. .. .d a.txt flushed" {
		t.Fatalf("restored content = %q", got)
	}
}

func waitBackup(t *testing.T, c *Core, id string, done func(store.Backup) bool) store.Backup {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		b, err := c.store.GetBackup(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if done(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup %s did not finish: %+v", id, b)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

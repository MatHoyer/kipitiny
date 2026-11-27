package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
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
	c.useDocker(dk)
	if _, err := c.EncryptBackupTarget(ctx, domain.LocalTargetID, ""); err != nil {
		t.Fatal(err)
	}

	p, err := c.store.CreateProject(ctx, domain.Project{Name: "voltest", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "busybox:stable",
		Replicas: 1, Volumes: []domain.Volume{{Name: "uploads", Path: "/uploads"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, "D1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.RemoveServiceVolumes(context.Background(), dk, svc) })

	// A running replica writing to the volume, stopped during the restore.
	replica, err := dk.Run(ctx, domain.ContainerSpec{
		Name:  "kipitiny-test-" + strings.ToLower(svc.ID),
		Image: "busybox:stable",
		Cmd:   []string{"sh", "-c", "[ -f /uploads/a.txt ] || { echo v1 > /uploads/a.txt && mkdir /uploads/.d && chown 1000:1000 /uploads/a.txt; }; sleep 3600"},
		// Not kipitiny.managed: a manager running on this daemon would
		// remove it as an orphan.
		Labels: map[string]string{domain.LabelProject: p.ID, domain.LabelService: svc.ID, domain.LabelDeploy: "D1"},
		Mounts: []domain.Mount{data.AppVolumeMount(svc, svc.Volumes[0].Name, svc.Volumes[0].Path)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainerAndVolumes(context.Background(), replica) })
	time.Sleep(time.Second)
	exec := func(cmd string) string {
		t.Helper()
		var out strings.Builder
		if err := dk.Exec(ctx, replica, domain.ExecOptions{Cmd: []string{"sh", "-c", cmd}, Stdout: &out}); err != nil {
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
	b = waitBackup(t, c, b.ID, func(b domain.Backup) bool { return b.Status != domain.OpRunning })
	if b.Status != domain.OpSucceeded || b.Kind != domain.BackupKindVolume || !b.Encrypted || b.SizeBytes == 0 {
		t.Fatalf("backup = %+v", b)
	}

	if _, err := c.VerifyBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b domain.Backup) bool { return b.VerifyStatus != domain.OpRunning })
	if b.VerifyStatus != domain.OpSucceeded || b.VerifyDetails.Files < 3 {
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
		if rs[0].ID == r.ID && rs[0].Status != domain.OpRunning {
			if rs[0].Status != domain.OpSucceeded {
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

func waitBackup(t *testing.T, c *Core, id string, done func(domain.Backup) bool) domain.Backup {
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

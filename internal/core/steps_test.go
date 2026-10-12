package core

import (
	"context"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestBackupSteps(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	b, err := c.store.CreateBackup(ctx, store.Backup{Kind: store.BackupKindManager, TargetID: "local", Status: store.OpRunning})
	if err != nil {
		t.Fatal(err)
	}

	step := c.steps.set(backupKey(b.ID), BackupUpload)
	if _, err := step.Write(make([]byte, 42)); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetBackup(ctx, b.ID); err != nil || got.Step != BackupUpload || got.StepBytes != 42 {
		t.Errorf("running: step = %q, bytes = %d, %v", got.Step, got.StepBytes, err)
	}
	c.steps.done(backupKey(b.ID))
	if got, _ := c.GetBackup(ctx, b.ID); got.Step != "" || got.StepBytes != 0 {
		t.Errorf("done: step = %q, bytes = %d", got.Step, got.StepBytes)
	}

	// A restore test reports its own step, apart from the backup's.
	b.Status = store.OpSucceeded
	if err := c.store.FinishBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetBackupVerification(ctx, b.ID, store.Verification{VerifyStatus: store.OpRunning}); err != nil {
		t.Fatal(err)
	}
	c.steps.set(verifyKey(b.ID), VerifyRestore)
	if got, err := c.GetBackup(ctx, b.ID); err != nil || got.VerifyStep != VerifyRestore || got.Step != "" {
		t.Errorf("restore test: step = %q, verify step = %q, %v", got.Step, got.VerifyStep, err)
	}
}

func TestCleanupSteps(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	c.cleaning.Store(true)
	c.steps.setOn(cleanupKey, CleanupImages, "edge-1")
	if v := c.Cleanup(ctx); v.Step != CleanupImages || v.StepServer != "edge-1" {
		t.Errorf("running: step = %q on %q", v.Step, v.StepServer)
	}
	c.cleaning.Store(false)
	if v := c.Cleanup(ctx); v.Step != "" || v.StepServer != "" {
		t.Errorf("idle: step = %q on %q", v.Step, v.StepServer)
	}
}

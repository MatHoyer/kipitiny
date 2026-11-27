package data

import (
	"context"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestBackupSteps(t *testing.T) {
	ctx := context.Background()
	c := newTestData(t, config.Config{}, nil)
	b, err := c.store.CreateBackup(ctx, domain.Backup{Kind: domain.BackupKindManager, TargetID: "local", Status: domain.OpRunning})
	if err != nil {
		t.Fatal(err)
	}

	step := c.steps.Set(backupKey(b.ID), BackupUpload)
	if _, err := step.Write(make([]byte, 42)); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetBackup(ctx, b.ID); err != nil || got.Step != BackupUpload || got.StepBytes != 42 {
		t.Errorf("running: step = %q, bytes = %d, %v", got.Step, got.StepBytes, err)
	}
	c.steps.Done(backupKey(b.ID))
	if got, _ := c.GetBackup(ctx, b.ID); got.Step != "" || got.StepBytes != 0 {
		t.Errorf("done: step = %q, bytes = %d", got.Step, got.StepBytes)
	}

	// A restore test reports its own step, apart from the backup's.
	b.Status = domain.OpSucceeded
	if err := c.store.FinishBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetBackupVerification(ctx, b.ID, domain.Verification{VerifyStatus: domain.OpRunning}); err != nil {
		t.Fatal(err)
	}
	c.steps.Set(verifyKey(b.ID), VerifyRestore)
	if got, err := c.GetBackup(ctx, b.ID); err != nil || got.VerifyStep != VerifyRestore || got.Step != "" {
		t.Errorf("restore test: step = %q, verify step = %q, %v", got.Step, got.VerifyStep, err)
	}
}

package core

import (
	"context"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestBackupVerifyStep(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	b, err := c.store.CreateBackup(ctx, store.Backup{Kind: store.BackupKindManager, TargetID: "local", Status: store.OpSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetBackupVerification(ctx, b.ID, store.Verification{VerifyStatus: store.OpRunning}); err != nil {
		t.Fatal(err)
	}
	c.verifySteps.set(b.ID, VerifyRestore)
	if got, err := c.GetBackup(ctx, b.ID); err != nil || got.VerifyStep != VerifyRestore {
		t.Errorf("running: step = %q, %v", got.VerifyStep, err)
	}
	c.verifySteps.done(b.ID)
	if got, _ := c.GetBackup(ctx, b.ID); got.VerifyStep != "" {
		t.Errorf("done: step = %q", got.VerifyStep)
	}
}

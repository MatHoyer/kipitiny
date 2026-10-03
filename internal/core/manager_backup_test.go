package core

import (
	"context"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSeedManagerSchedule(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{ManagerBackup: config.ManagerBackup{Cron: "@daily", TargetID: "gone", Keep: 14}})
	for range 2 { // seeded once, even across restarts
		if err := c.seedManagerSchedule(ctx); err != nil {
			t.Fatal(err)
		}
	}
	scs, err := c.ListManagerSchedules(ctx)
	if err != nil || len(scs) != 1 {
		t.Fatalf("manager schedules: %+v %v", scs, err)
	}
	sc := scs[0]
	if sc.Cron != "@daily" || sc.KeepLast != 14 || sc.TargetID != store.LocalTargetID || sc.Verify || !sc.Enabled {
		t.Fatalf("seeded schedule: %+v", sc)
	}

	// Deleted in the UI, it stays deleted.
	if err := c.DeleteBackupSchedule(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.seedManagerSchedule(ctx); err != nil {
		t.Fatal(err)
	}
	if scs, _ := c.ListManagerSchedules(ctx); len(scs) != 0 {
		t.Fatalf("reseeded: %+v", scs)
	}
}

func TestSeedManagerScheduleOff(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if err := c.seedManagerSchedule(ctx); err != nil {
		t.Fatal(err)
	}
	if scs, _ := c.ListManagerSchedules(ctx); len(scs) != 0 {
		t.Fatalf("schedule created with KIPITINY_MANAGER_BACKUP_CRON=off: %+v", scs)
	}
}

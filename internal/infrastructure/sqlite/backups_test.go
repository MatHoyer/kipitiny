package sqlite

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/anonymize"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestBackups(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	targets, err := s.ListBackupTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID != domain.LocalTargetID {
		t.Fatalf("seeded local target: %+v %v", targets, err)
	}
	s3, err := s.CreateBackupTarget(ctx, domain.BackupTarget{
		Name: "offsite", Kind: domain.BackupTargetS3, Endpoint: "s3.example.com", Bucket: "b", SecretKey: "k", UseSSL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s3.Prefix = "pg/"
	if _, err := s.UpdateBackupTarget(ctx, s3); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackupTarget(ctx, s3.ID); got.Prefix != "pg/" || !got.UseSSL {
		t.Fatalf("target round-trip: %+v", got)
	}

	b, err := s.CreateBackup(ctx, domain.Backup{
		ServiceID: "SVC", ProjectID: "P", ServiceName: "db", ProjectName: "shop",
		TargetID: s3.ID, ObjectKey: "shop/db/x.dump", Status: domain.OpRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.CreateBackup(ctx, domain.Backup{
		Kind: domain.BackupKindVolume, ServiceID: "SVC2", ProjectID: "P", ServiceName: "web", ProjectName: "shop",
		ServiceKind: domain.ServiceKindRedis, ServiceIcon: "redis", TargetID: domain.LocalTargetID, ObjectKey: "shop/web/x.tar.gz", Status: domain.OpRunning, Volumes: []string{"uploads", "cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetBackup(ctx, vb.ID); err != nil || got.Kind != domain.BackupKindVolume || got.ServiceKind != domain.ServiceKindRedis || got.ServiceIcon != "redis" || !slices.Equal(got.Volumes, vb.Volumes) {
		t.Fatalf("volume backup round-trip: %+v %v", got, err)
	}
	if err := s.DeleteBackup(ctx, vb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupTarget(ctx, s3.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deleting a target in use: %v", err)
	}

	b.Status, b.SizeBytes, b.SHA256, b.PGVersion, b.DurationMS, b.Encrypted = domain.OpSucceeded, 1234, "abc", "17.2", 99, true
	if err := s.FinishBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackup(ctx, b.ID)
	if err != nil || got.SizeBytes != 1234 || got.PGVersion != "17.2" || got.FinishedAt == nil || !got.Encrypted {
		t.Fatalf("backup round-trip: %+v %v", got, err)
	}

	at := time.Now()
	v := domain.Verification{VerifyStatus: domain.OpSucceeded, VerifiedAt: &at,
		VerifyDetails: domain.VerificationDetails{Tables: 3, Rows: 42}}
	if err := s.SetBackupVerification(ctx, b.ID, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackup(ctx, b.ID); got.VerifyStatus != domain.OpSucceeded || got.VerifyDetails.Rows != 42 || got.VerifiedAt == nil || got.SizeBytes != 1234 {
		t.Fatalf("verification round-trip: %+v", got)
	}

	running, _ := s.CreateBackup(ctx, domain.Backup{ServiceID: "SVC", TargetID: "local", Status: domain.OpRunning})
	if err := s.FailRunningOperations(ctx, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackup(ctx, running.ID); got.Status != domain.OpFailed {
		t.Fatalf("running backup not failed: %+v", got)
	}
	ok, err := s.ListBackups(ctx, domain.BackupFilter{ServiceID: "SVC", Status: domain.OpSucceeded})
	if err != nil || len(ok) != 1 {
		t.Fatalf("filtered list: %v %v", ok, err)
	}

	if err := s.DeleteBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackup(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupTarget(ctx, s3.ID); err != nil {
		t.Fatalf("delete unused target: %v", err)
	}
}

func TestBackupSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, _ := s.CreateProject(ctx, domain.Project{Name: "shop"})
	db, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "db", Kind: domain.ServiceKindPostgres, Image: "postgres:17"})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.CreateBackupSchedule(ctx, domain.BackupSchedule{
		ServiceID: db.ID, TargetID: domain.LocalTargetID, Cron: "@daily", KeepDaily: 7, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sc.Enabled, sc.KeepWeekly = false, 4
	if _, err := s.UpdateBackupSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupSchedule(ctx, sc.ID)
	if err != nil || got.Enabled || got.KeepWeekly != 4 || got.KeepDaily != 7 {
		t.Fatalf("schedule round-trip: %+v %v", got, err)
	}
	if _, err := s.CreateBackup(ctx, domain.Backup{ServiceID: db.ID, TargetID: "local", ScheduleID: sc.ID, Status: domain.OpSucceeded}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.ListBackups(ctx, domain.BackupFilter{ScheduleID: sc.ID}); len(bs) != 1 {
		t.Fatalf("filter by schedule: %v", bs)
	}
	// Deleting the database removes its schedules (backups stay).
	if err := s.DeleteService(ctx, db.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.ListBackupSchedules(ctx, ""); len(all) != 0 {
		t.Fatalf("schedules not cascaded: %v", all)
	}
	if bs, _ := s.ListBackups(ctx, domain.BackupFilter{}); len(bs) != 1 {
		t.Fatalf("backups must outlive the database: %v", bs)
	}
}

func TestManagerSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	sc, err := s.CreateBackupSchedule(ctx, domain.BackupSchedule{
		Kind: domain.BackupKindManager, TargetID: domain.LocalTargetID, Cron: "@daily", KeepLast: 14, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupSchedule(ctx, sc.ID)
	if err != nil || got.Kind != domain.BackupKindManager || got.ServiceID != "" {
		t.Fatalf("manager schedule round-trip: %+v %v", got, err)
	}
	// A manager schedule has no service, a database schedule needs one.
	if _, err := s.CreateBackupSchedule(ctx, domain.BackupSchedule{TargetID: domain.LocalTargetID, Cron: "@daily"}); err == nil {
		t.Fatal("database schedule without a service accepted")
	}
}

func TestSetBackupTargetKey(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.SetBackupTargetKey(ctx, domain.LocalTargetID, "AGE-SECRET-KEY-1", "age1one"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupTarget(ctx, domain.LocalTargetID)
	if err != nil || got.AgeRecipient != "age1one" || got.AgeIdentity != "AGE-SECRET-KEY-1" {
		t.Fatalf("key not saved: %+v %v", got, err)
	}
	// A key is never replaced: backups encrypted with it would be lost.
	if err := s.SetBackupTargetKey(ctx, domain.LocalTargetID, "AGE-SECRET-KEY-2", "age1two"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second key: %v", err)
	}
	if got, _ := s.GetBackupTarget(ctx, domain.LocalTargetID); got.AgeRecipient != "age1one" {
		t.Fatalf("key replaced: %+v", got)
	}
	if err := s.SetBackupTargetKey(ctx, "missing", "k", "r"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestAnonymizeRulesAndCopies(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, domain.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "db", Kind: domain.ServiceKindPostgres, Image: "postgres:17"})
	if err != nil {
		t.Fatal(err)
	}
	// nil has no rules, an empty set holds nothing personal: both survive.
	if got, _ := s.GetService(ctx, db.ID); got.Anonymize != nil {
		t.Fatalf("new service has rules %#v", got.Anonymize)
	}
	if err := s.SetServiceAnonymize(ctx, db.ID, anonymize.Rules{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, db.ID); got.Anonymize == nil || len(got.Anonymize) != 0 {
		t.Fatalf("empty rules came back as %#v", got.Anonymize)
	}
	rules := anonymize.Rules{"users": {Columns: map[string]string{"email": "fake:email"}}, "sessions": {Action: anonymize.Truncate}}
	if err := s.SetServiceAnonymize(ctx, db.ID, rules); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, db.ID); got.Anonymize.Hash("00") != rules.Hash("00") {
		t.Fatalf("rules came back as %#v", got.Anonymize)
	}
	if err := s.SetServiceAnonymize(ctx, db.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, db.ID); got.Anonymize != nil {
		t.Fatalf("cleared rules came back as %#v", got.Anonymize)
	}

	src, err := s.CreateBackup(ctx, domain.Backup{ServiceID: db.ID, ProjectID: p.ID, TargetID: domain.LocalTargetID, ObjectKey: "a.dump", Status: domain.OpSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateBackup(ctx, domain.Backup{ServiceID: db.ID, ProjectID: p.ID, TargetID: domain.LocalTargetID, ObjectKey: "a.anon.dump",
		Status: domain.OpSucceeded, SourceID: src.ID, RulesHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.ListBackups(ctx, domain.BackupFilter{ServiceID: db.ID}); len(bs) != 1 || bs[0].ID != src.ID {
		t.Fatalf("backups list copies: %+v", bs)
	}
	if bs, _ := s.ListBackups(ctx, domain.BackupFilter{ServiceID: db.ID, Anonymized: true, SourceID: src.ID}); len(bs) != 1 || bs[0].ID != cp.ID || bs[0].RulesHash != "h" {
		t.Fatalf("copies: %+v", bs)
	}

	ps, err := s.SavePreviewSettings(ctx, domain.PreviewSettings{ProjectID: p.ID, SeedMode: domain.SeedAnonymized, Salt: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	ps.SeedMode = domain.SeedRaw
	if _, err := s.SavePreviewSettings(ctx, ps); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetPreviewSettings(ctx, p.ID); got.SeedMode != domain.SeedRaw || got.Salt != "abc" {
		t.Fatalf("preview settings %+v", got)
	}
}

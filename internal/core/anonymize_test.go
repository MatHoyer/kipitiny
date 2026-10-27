package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/anonymize"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSeedAnonymized(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	f, _, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := c.store.SavePreviewSettings(ctx, store.PreviewSettings{ProjectID: parent.ID, SeedMode: store.SeedAnonymized, Salt: anonymize.NewSalt()})
	if err != nil {
		t.Fatal(err)
	}
	db, _ := c.serviceNamed(ctx, parent.ID, "db")
	seed := func() (map[string]string, error) {
		svcs, _ := c.store.ListServices(ctx, parent.ID)
		return c.seedAnonymized(ctx, parent, f, svcs)
	}
	refused := func(want string) {
		t.Helper()
		if seeds, err := seed(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("seeds %v, err %v; want %q", seeds, err, want)
		}
	}
	backup := func(b store.Backup) store.Backup {
		t.Helper()
		b.Kind, b.ServiceID, b.ProjectID, b.TargetID, b.ServiceKind = store.BackupKindPostgres, db.ID, parent.ID, store.LocalTargetID, db.Kind
		b.ObjectKey = "k" + string(b.Status) + b.RulesHash
		b, err := c.store.CreateBackup(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	refused("no backup of db")
	src := backup(store.Backup{Status: store.OpSucceeded})
	refused("no anonymized copy yet")
	hash := db.Anonymize.Hash(ps.Salt)
	running := backup(store.Backup{Status: store.OpRunning, SourceID: src.ID, RulesHash: hash})
	refused("still being made")
	if err := c.store.DeleteBackup(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	backup(store.Backup{Status: store.OpFailed, SourceID: src.ID, RulesHash: hash, Error: "no table users"})
	refused("failed: no table users")
	// A good copy made with other rules (or another salt) doesn't count.
	backup(store.Backup{Status: store.OpSucceeded, SourceID: src.ID, RulesHash: "other"})
	refused("failed")
	good := backup(store.Backup{Status: store.OpSucceeded, SourceID: src.ID, RulesHash: hash})
	if seeds, err := seed(); err != nil || seeds["db"] != good.ID {
		t.Fatalf("seeds %v, %v", seeds, err)
	}

	// Without rules, never the raw backup.
	if err := c.store.SetServiceAnonymize(ctx, db.ID, nil); err != nil {
		t.Fatal(err)
	}
	refused("db has no anonymize rules")
}

func TestSetAnonymizeRules(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	if _, err := c.SetAnonymizeRules(ctx, parent.ID, "db", anonymize.Rules{"t": {Action: "drop"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad rule: %v", err)
	}
	if _, err := c.SetAnonymizeRules(ctx, parent.ID, "web", anonymize.Rules{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an app: %v", err)
	}
	d, err := c.SetAnonymizeRules(ctx, parent.ID, "db", anonymize.Rules{})
	if err != nil || d.Rules == nil || d.Name != "db" {
		t.Fatalf("set: %+v, %v", d, err)
	}
	dbs, err := c.PreviewSeed(ctx, parent.ID)
	if err != nil || len(dbs) != 1 || dbs[0].Rules == nil {
		t.Fatalf("seed status: %+v, %v", dbs, err)
	}
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: parent.ID, RepoURL: "https://example.com/r.git", Branch: "main", Path: "compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetAnonymizeRules(ctx, parent.ID, "db", nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "x-kipitiny.anonymize of compose.yaml") {
		t.Errorf("git project: %v", err)
	}
}

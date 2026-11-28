package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

func TestScanBackupTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir() // the storage both managers use
	ageIdentity, recipient, _ := age.Encryption{}.NewKey()
	target := domain.BackupTarget{AgeRecipient: recipient, AgeIdentity: ageIdentity}
	st := &storage.Local{Root: dir + "/backups"}
	created := time.Date(2026, 9, 30, 3, 0, 0, 0, time.UTC)

	// What the old manager left: a backup with its record per case.
	old := newTestData(t, config.Config{DataDir: t.TempDir()}, nil)
	put := func(b domain.Backup) {
		t.Helper()
		var sealed strings.Builder
		w, _ := age.Encryption{}.Encrypt(&sealed, recipient)
		if _, err := w.Write([]byte("dump")); err != nil {
			t.Fatal(err)
		}
		w.Close()
		b.Status, b.Encrypted, b.CreatedAt = domain.OpSucceeded, true, created
		if b.SizeBytes == 0 {
			b.SizeBytes = int64(sealed.Len())
		}
		if err := st.Put(ctx, b.ObjectKey, strings.NewReader(sealed.String())); err != nil {
			t.Fatal(err)
		}
		if err := old.writeSidecar(ctx, st, target, b); err != nil {
			t.Fatal(err)
		}
	}
	put(domain.Backup{Kind: domain.BackupKindPostgres, ServiceKind: domain.ServiceKindPostgres, ProjectName: "shop", ServiceName: "db",
		ObjectKey: "shop/db/a.dump.age", PGVersion: "17.2", ScheduleID: "old"})
	put(domain.Backup{Kind: domain.BackupKindPostgres, ServiceKind: domain.ServiceKindPostgres, ProjectName: "shop", ServiceName: "web",
		ObjectKey: "shop/web/b.dump.age"}) // web is an app here
	put(domain.Backup{Kind: domain.BackupKindDump, ServiceKind: domain.ServiceKindMySQL, ProjectName: "gone", ServiceName: "sql",
		ObjectKey: "gone/sql/c.sql.gz.age"}) // no such project
	put(domain.Backup{Kind: domain.BackupKindPostgres, ProjectName: "shop", ServiceName: "db",
		ObjectKey: "shop/db/d.dump.age", SizeBytes: 99}) // truncated
	if err := st.Put(ctx, "shop/db/older.dump", strings.NewReader("no record")); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "deleted.dump.age.json", strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}

	c := newTestData(t, config.Config{DataDir: dir}, nil)
	p, _ := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	db, _ := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "db", Kind: domain.ServiceKindPostgres, Image: "postgres:17", Replicas: 1})
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EncryptBackupTarget(ctx, domain.LocalTargetID, ageIdentity); err != nil {
		t.Fatal(err)
	}

	res, err := c.ScanBackupTarget(ctx, domain.LocalTargetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Adopted) != 3 || res.Orphans != 2 || res.Known != 0 || res.NoRecord != 1 || len(res.Unreadable) != 1 ||
		res.Unreadable[0].Key != "shop/db/d.dump.age.json" {
		t.Fatalf("scan: %+v", res)
	}
	bs, _ := c.store.ListBackups(ctx, domain.BackupFilter{ServiceID: db.ID})
	if len(bs) != 1 {
		t.Fatalf("db backups: %+v", bs)
	}
	b := bs[0]
	if b.ProjectID != p.ID || b.TargetID != domain.LocalTargetID || b.ScheduleID != "" || !b.CreatedAt.Equal(created) ||
		b.PGVersion != "17.2" || b.ObjectKey != "shop/db/a.dump.age" || b.Status != domain.OpSucceeded || b.FinishedAt == nil {
		t.Fatalf("adopted: %+v", b)
	}
	for _, o := range res.Adopted {
		if o.ServiceName == "web" && o.ServiceID != "" {
			t.Errorf("dump attached to an app: %+v", o)
		}
	}

	// Scanning again adopts nothing twice.
	res, err = c.ScanBackupTarget(ctx, domain.LocalTargetID)
	if err != nil || len(res.Adopted) != 0 || res.Known != 3 || res.NoRecord != 1 {
		t.Fatalf("rescan: %+v %v", res, err)
	}

	// Unencrypted, the storage can't read the records.
	plain := newTestData(t, config.Config{DataDir: dir}, nil)
	res, err = plain.ScanBackupTarget(ctx, domain.LocalTargetID)
	if err != nil || len(res.Adopted) != 0 || len(res.Unreadable) != 4 {
		t.Fatalf("without the key: %+v %v", res, err)
	}
}

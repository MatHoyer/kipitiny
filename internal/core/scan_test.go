package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestScanBackupTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir() // the storage both managers use
	identity, recipient, _ := newAgeKey()
	target := store.BackupTarget{AgeRecipient: recipient, AgeIdentity: identity}
	st := &storage.Local{Root: dir + "/backups"}
	created := time.Date(2026, 9, 30, 3, 0, 0, 0, time.UTC)

	// What the old manager left: a backup with its record per case.
	put := func(b store.Backup) {
		t.Helper()
		var sealed strings.Builder
		w, _ := encryptTo(&sealed, recipient)
		w.Write([]byte("dump"))
		w.Close()
		b.Status, b.Encrypted, b.CreatedAt = store.OpSucceeded, true, created
		if b.SizeBytes == 0 {
			b.SizeBytes = int64(sealed.Len())
		}
		if err := st.Put(ctx, b.ObjectKey, strings.NewReader(sealed.String())); err != nil {
			t.Fatal(err)
		}
		if err := writeSidecar(ctx, st, target, b); err != nil {
			t.Fatal(err)
		}
	}
	put(store.Backup{Kind: store.BackupKindPostgres, ServiceKind: store.ServiceKindPostgres, ProjectName: "shop", ServiceName: "db",
		ObjectKey: "shop/db/a.dump.age", PGVersion: "17.2", ScheduleID: "old"})
	put(store.Backup{Kind: store.BackupKindPostgres, ServiceKind: store.ServiceKindPostgres, ProjectName: "shop", ServiceName: "web",
		ObjectKey: "shop/web/b.dump.age"}) // web is an app here
	put(store.Backup{Kind: store.BackupKindDump, ServiceKind: store.ServiceKindMySQL, ProjectName: "gone", ServiceName: "sql",
		ObjectKey: "gone/sql/c.sql.gz.age"}) // no such project
	put(store.Backup{Kind: store.BackupKindPostgres, ProjectName: "shop", ServiceName: "db",
		ObjectKey: "shop/db/d.dump.age", SizeBytes: 99}) // truncated
	if err := st.Put(ctx, "shop/db/older.dump", strings.NewReader("no record")); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "deleted.dump.age.json", strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}

	c := newTestCore(t, config.Config{DataDir: dir})
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	db, _ := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Replicas: 1})
	c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if _, err := c.EncryptBackupTarget(ctx, store.LocalTargetID, identity); err != nil {
		t.Fatal(err)
	}

	res, err := c.ScanBackupTarget(ctx, store.LocalTargetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Adopted) != 3 || res.Orphans != 2 || res.Known != 0 || res.NoRecord != 1 || len(res.Unreadable) != 1 ||
		res.Unreadable[0].Key != "shop/db/d.dump.age.json" {
		t.Fatalf("scan: %+v", res)
	}
	bs, _ := c.store.ListBackups(ctx, store.BackupFilter{ServiceID: db.ID})
	if len(bs) != 1 {
		t.Fatalf("db backups: %+v", bs)
	}
	b := bs[0]
	if b.ProjectID != p.ID || b.TargetID != store.LocalTargetID || b.ScheduleID != "" || !b.CreatedAt.Equal(created) ||
		b.PGVersion != "17.2" || b.ObjectKey != "shop/db/a.dump.age" || b.Status != store.OpSucceeded || b.FinishedAt == nil {
		t.Fatalf("adopted: %+v", b)
	}
	for _, o := range res.Adopted {
		if o.ServiceName == "web" && o.ServiceID != "" {
			t.Errorf("dump attached to an app: %+v", o)
		}
	}

	// Scanning again adopts nothing twice.
	res, err = c.ScanBackupTarget(ctx, store.LocalTargetID)
	if err != nil || len(res.Adopted) != 0 || res.Known != 3 || res.NoRecord != 1 {
		t.Fatalf("rescan: %+v %v", res, err)
	}

	// Unencrypted, the storage can't read the records.
	plain := newTestCore(t, config.Config{DataDir: dir})
	res, err = plain.ScanBackupTarget(ctx, store.LocalTargetID)
	if err != nil || len(res.Adopted) != 0 || len(res.Unreadable) != 4 {
		t.Fatalf("without the key: %+v %v", res, err)
	}
}

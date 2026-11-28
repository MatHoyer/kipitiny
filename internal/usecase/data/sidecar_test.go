package data

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/storage"
)

func TestSidecar(t *testing.T) {
	ctx := context.Background()
	c := newTestData(t, config.Config{DataDir: t.TempDir()}, nil)
	ageIdentity, recipient, _ := age.Encryption{}.NewKey()
	created := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	b := domain.Backup{
		ID: "b1", ServiceID: "s1", ProjectID: "p1", TargetID: "t1", ScheduleID: "sc1",
		Kind: domain.BackupKindPostgres, ProjectName: "shop", ServiceName: "db", ServiceKind: domain.ServiceKindPostgres,
		Database: "app", ObjectKey: "shop/db/20261001T030000Z-abcd.dump", Status: domain.OpSucceeded,
		SizeBytes: 42, SHA256: "ff", PGVersion: "17.2", Image: "postgres:17", Extensions: []string{"vector"},
		CreatedAt: created, Verification: domain.Verification{VerifyStatus: domain.OpSucceeded},
	}
	for _, target := range []domain.BackupTarget{{}, {AgeRecipient: recipient, AgeIdentity: ageIdentity}} {
		st := &storage.Local{Root: t.TempDir()}
		if err := c.writeSidecar(ctx, st, target, b); err != nil {
			t.Fatal(err)
		}
		rc, _ := st.Get(ctx, b.ObjectKey+".json")
		raw, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(raw), "shop") == target.Encrypted() {
			t.Errorf("encrypted %v, stored %.40q", target.Encrypted(), raw)
		}

		got, err := c.readSidecar(ctx, st, b.ObjectKey+".json", ageIdentity)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "" || got.ServiceID != "" || got.ProjectID != "" || got.TargetID != "" || got.ScheduleID != "" || got.VerifyStatus != "" {
			t.Errorf("manager-specific fields kept: %+v", got)
		}
		if got.ProjectName != "shop" || got.ServiceName != "db" || got.Database != "app" || got.SHA256 != "ff" ||
			got.SizeBytes != 42 || got.PGVersion != "17.2" || got.Extensions[0] != "vector" || !got.CreatedAt.Equal(created) {
			t.Errorf("record: %+v", got)
		}
		if target.Encrypted() {
			other, _, _ := age.Encryption{}.NewKey()
			if _, err := c.readSidecar(ctx, st, b.ObjectKey+".json", other); err == nil {
				t.Error("read with the wrong key")
			}
		}
	}
}

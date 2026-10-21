package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestEncryptWithExistingKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := newTestCore(t, config.Config{DataDir: dir})
	mine, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()

	// A backup another manager encrypted with mine.
	st := &storage.Local{Root: dir + "/backups"}
	var sealed strings.Builder
	w, _ := age.Encrypt(&sealed, mine.Recipient())
	w.Write([]byte("pg_dump output"))
	w.Close()
	if err := st.Put(ctx, "shop/db/a.dump", strings.NewReader("plain, older")); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "shop/db/b.dump.age", strings.NewReader(sealed.String())); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"not a key", other.String()} {
		if _, err := c.EncryptBackupTarget(ctx, store.LocalTargetID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("key %.20q accepted: %v", bad, err)
		}
	}
	tg, err := c.EncryptBackupTarget(ctx, store.LocalTargetID, "  "+mine.String()+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if tg.AgeRecipient != mine.Recipient().String() {
		t.Fatalf("recipient %q", tg.AgeRecipient)
	}
	if k, _ := c.BackupTargetKey(ctx, store.LocalTargetID); k.Identity != mine.String() {
		t.Fatalf("stored identity %q", k.Identity)
	}
	// The key can't be replaced, even by a valid one.
	if _, err := c.EncryptBackupTarget(ctx, store.LocalTargetID, mine.String()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("re-encrypted: %v", err)
	}

	// Nothing encrypted yet: any key will do.
	empty := newTestCore(t, config.Config{DataDir: t.TempDir()})
	if tg, err := empty.EncryptBackupTarget(ctx, store.LocalTargetID, other.String()); err != nil || tg.AgeRecipient != other.Recipient().String() {
		t.Fatalf("empty storage: %+v %v", tg, err)
	}
}

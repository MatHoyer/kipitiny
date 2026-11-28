package data

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

func TestUploadEncrypted(t *testing.T) {
	ctx := context.Background()
	c := newTestData(t, config.Config{DataDir: t.TempDir()}, nil)
	st := &storage.Local{Root: t.TempDir()}
	ageIdentity, recipient, err := age.Encryption{}.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	target := domain.BackupTarget{Kind: domain.BackupTargetLocal, AgeRecipient: recipient, AgeIdentity: ageIdentity}
	plain := strings.Repeat("pg_dump output ", 100_000)

	b := &domain.Backup{ObjectKey: "a.dump.age"}
	err = c.upload(ctx, st, target, b, func(w io.Writer) error {
		_, err := io.WriteString(w, plain)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Encrypted || b.SizeBytes == 0 || b.SHA256 == "" {
		t.Fatalf("metadata: %+v", b)
	}

	rc, _ := st.Get(ctx, "a.dump.age")
	stored, _ := io.ReadAll(rc)
	rc.Close()
	if bytes.Contains(stored, []byte("pg_dump output")) {
		t.Fatal("stored object is not encrypted")
	}
	dec, err := c.decryptFrom(bytes.NewReader(stored), ageIdentity)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil || string(got) != plain {
		t.Fatalf("round-trip failed: %v (len %d)", err, len(got))
	}

	other, _, _ := age.Encryption{}.NewKey()
	if dec, err := c.decryptFrom(bytes.NewReader(stored), other); err == nil {
		if _, err := io.ReadAll(dec); err == nil {
			t.Fatal("decrypted with the wrong key")
		}
	}
}

func TestUploadProducerFailureWins(t *testing.T) {
	ctx := context.Background()
	c := newTestData(t, config.Config{}, nil)
	st := &storage.Local{Root: t.TempDir()}
	b := &domain.Backup{ObjectKey: "a.dump"}
	boom := errors.New("pg_dump exploded")
	err := c.upload(ctx, st, domain.BackupTarget{}, b, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial data"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the producer's error", err)
	}
	if _, err := st.Get(ctx, "a.dump"); err == nil {
		t.Fatal("partial object left behind")
	}
}

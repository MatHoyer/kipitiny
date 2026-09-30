package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestUploadEncrypted(t *testing.T) {
	ctx := context.Background()
	c := New(config.Config{DataDir: t.TempDir()}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st := &storage.Local{Root: t.TempDir()}
	identity, recipient, err := newAgeKey()
	if err != nil {
		t.Fatal(err)
	}
	target := store.BackupTarget{Kind: store.BackupTargetLocal, AgeRecipient: recipient, AgeIdentity: identity}
	plain := strings.Repeat("pg_dump output ", 100_000)

	b := &store.Backup{ObjectKey: "a.dump.age"}
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
	dec, err := decryptFrom(bytes.NewReader(stored), identity)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil || string(got) != plain {
		t.Fatalf("round-trip failed: %v (len %d)", err, len(got))
	}

	other, _, _ := newAgeKey()
	if dec, err := decryptFrom(bytes.NewReader(stored), other); err == nil {
		if _, err := io.ReadAll(dec); err == nil {
			t.Fatal("decrypted with the wrong key")
		}
	}
}

func TestUploadProducerFailureWins(t *testing.T) {
	ctx := context.Background()
	c := New(config.Config{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st := &storage.Local{Root: t.TempDir()}
	b := &store.Backup{ObjectKey: "a.dump"}
	boom := errors.New("pg_dump exploded")
	err := c.upload(ctx, st, store.BackupTarget{}, b, func(w io.Writer) error {
		io.WriteString(w, "partial data")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the producer's error", err)
	}
	if _, err := st.Get(ctx, "a.dump"); err == nil {
		t.Fatal("partial object left behind")
	}
}

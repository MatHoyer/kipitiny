package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type TargetInput struct {
	// Kind is set on creation only; empty means s3.
	Kind      store.BackupTargetKind `json:"kind"`
	Name      string                 `json:"name"`
	Endpoint  string                 `json:"endpoint"`
	Region    string                 `json:"region"`
	Bucket    string                 `json:"bucket"`
	Prefix    string                 `json:"prefix"`
	AccessKey string                 `json:"accessKey"`
	// SecretKey equal to SecretMask keeps the stored value on update.
	SecretKey string `json:"secretKey"`
	UseSSL    bool   `json:"useSsl"`
	// Encrypt generates an age key for a new target; existing ones use
	// EncryptBackupTarget.
	Encrypt bool `json:"encrypt"`
}

func (c *Core) openStorage(t store.BackupTarget) (storage.Storage, error) {
	return storage.Open(t, storage.Env{DataDir: c.cfg.DataDir})
}

func (c *Core) ListBackupTargets(ctx context.Context) ([]store.BackupTarget, error) {
	ts, err := c.store.ListBackupTargets(ctx)
	for i := range ts {
		ts[i] = maskedTarget(ts[i])
	}
	return ts, err
}

func (c *Core) CreateBackupTarget(ctx context.Context, in TargetInput) (store.BackupTarget, error) {
	if in.Kind == "" {
		in.Kind = store.BackupTargetS3
	}
	t := store.BackupTarget{Kind: in.Kind}
	if err := c.applyTargetInput(ctx, &t, in); err != nil {
		return store.BackupTarget{}, err
	}
	if err := c.checkTarget(ctx, &t); err != nil {
		return store.BackupTarget{}, err
	}
	if in.Encrypt {
		var err error
		if t.AgeIdentity, t.AgeRecipient, err = newAgeKey(); err != nil {
			return store.BackupTarget{}, err
		}
	}
	t, err := c.store.CreateBackupTarget(ctx, t)
	return maskedTarget(t), err
}

func (c *Core) UpdateBackupTarget(ctx context.Context, id string, in TargetInput) (store.BackupTarget, error) {
	t, err := c.store.GetBackupTarget(ctx, id)
	if err != nil {
		return store.BackupTarget{}, err
	}
	if t.Kind == store.BackupTargetLocal {
		return store.BackupTarget{}, fmt.Errorf("%w: the local target cannot be changed", ErrInvalid)
	}
	if err := c.applyTargetInput(ctx, &t, in); err != nil {
		return store.BackupTarget{}, err
	}
	if err := c.checkTarget(ctx, &t); err != nil {
		return store.BackupTarget{}, err
	}
	t, err = c.store.UpdateBackupTarget(ctx, t)
	return maskedTarget(t), err
}

func (c *Core) DeleteBackupTarget(ctx context.Context, id string) error {
	if id == store.LocalTargetID {
		return fmt.Errorf("%w: the local target cannot be deleted", ErrInvalid)
	}
	err := c.store.DeleteBackupTarget(ctx, id)
	if errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("%w: %v; delete them first", ErrInvalid, err)
	}
	return err
}

// EncryptBackupTarget gives a target (the local one too) an age key: backups
// made from now on are encrypted, earlier ones stay readable as they are.
// It can't be turned off: the key must outlive the backups it encrypted.
func (c *Core) EncryptBackupTarget(ctx context.Context, id string) (store.BackupTarget, error) {
	identity, recipient, err := newAgeKey()
	if err != nil {
		return store.BackupTarget{}, err
	}
	if err := c.store.SetBackupTargetKey(ctx, id, identity, recipient); errors.Is(err, store.ErrConflict) {
		return store.BackupTarget{}, fmt.Errorf("%w: this storage is already encrypted", ErrInvalid)
	} else if err != nil {
		return store.BackupTarget{}, err
	}
	t, err := c.store.GetBackupTarget(ctx, id)
	return maskedTarget(t), err
}

type TargetKey struct {
	Identity  string `json:"identity"`
	Recipient string `json:"recipient"`
}

// BackupTargetKey reveals a target's age key. Keep a copy offline: without
// it, encrypted backups can't be read if the manager's data is lost.
func (c *Core) BackupTargetKey(ctx context.Context, id string) (TargetKey, error) {
	t, err := c.store.GetBackupTarget(ctx, id)
	if err != nil {
		return TargetKey{}, err
	}
	if !t.Encrypted() {
		return TargetKey{}, fmt.Errorf("%w: target is not encrypted", ErrInvalid)
	}
	return TargetKey{Identity: t.AgeIdentity, Recipient: t.AgeRecipient}, nil
}

func (c *Core) applyTargetInput(ctx context.Context, t *store.BackupTarget, in TargetInput) error {
	t.Name = strings.TrimSpace(in.Name)
	t.Prefix = strings.Trim(strings.TrimSpace(in.Prefix), "/")
	if t.Kind != store.BackupTargetS3 {
		return fmt.Errorf("%w: unknown target kind %q", ErrInvalid, t.Kind)
	}
	t.Endpoint = strings.TrimSpace(in.Endpoint)
	// Accept pasted URLs; minio wants host[:port] and a separate TLS flag.
	if rest, ok := strings.CutPrefix(t.Endpoint, "https://"); ok {
		t.Endpoint, in.UseSSL = rest, true
	} else if rest, ok := strings.CutPrefix(t.Endpoint, "http://"); ok {
		t.Endpoint, in.UseSSL = rest, false
	}
	t.Endpoint = strings.TrimRight(t.Endpoint, "/")
	t.Region = strings.TrimSpace(in.Region)
	t.Bucket = strings.TrimSpace(in.Bucket)
	t.AccessKey = strings.TrimSpace(in.AccessKey)
	if in.SecretKey != SecretMask {
		t.SecretKey = in.SecretKey
	}
	t.UseSSL = in.UseSSL
	return nil
}

// checkTarget validates fields, then writes and deletes a test object so a
// misconfigured target fails now rather than at 3am.
func (c *Core) checkTarget(ctx context.Context, t *store.BackupTarget) error {
	if t.Name == "" {
		return fmt.Errorf("%w: a name is required", ErrInvalid)
	}
	if t.Endpoint == "" || t.Bucket == "" || t.AccessKey == "" || t.SecretKey == "" {
		return fmt.Errorf("%w: name, endpoint, bucket and credentials are required", ErrInvalid)
	}
	st, err := c.openStorage(*t)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := st.Check(ctx); err != nil {
		return fmt.Errorf("%w: cannot use bucket: %v", ErrInvalid, err)
	}
	return nil
}

// TestBackupTarget writes and deletes a test object on a saved target.
func (c *Core) TestBackupTarget(ctx context.Context, id string) error {
	t, err := c.store.GetBackupTarget(ctx, id)
	if err != nil {
		return err
	}
	st, err := c.openStorage(t)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := st.Check(ctx); err != nil {
		return fmt.Errorf("%w: cannot write to %s: %v", ErrInvalid, t.Name, err)
	}
	return nil
}

// maskedTarget hides credentials.
func maskedTarget(t store.BackupTarget) store.BackupTarget {
	if t.SecretKey != "" {
		t.SecretKey = SecretMask
	}
	return t
}

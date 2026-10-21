package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"filippo.io/age"

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
	// AgeIdentity, when set, encrypts with this existing key instead (an
	// AGE-SECRET-KEY-1… line): another manager's, to read its backups.
	AgeIdentity string `json:"ageIdentity"`
}

func (c *Core) openStorage(t store.BackupTarget) (storage.Storage, error) {
	return storage.Open(t, storage.Env{DataDir: c.cfg.DataDir})
}

func (c *Core) ListBackupTargets(ctx context.Context) ([]store.BackupTarget, error) {
	ts, err := c.store.ListBackupTargets(ctx)
	for i, t := range ts {
		ts[i] = maskedTarget(t)
		if isMember(ctx) { // enough to pick one
			ts[i] = store.BackupTarget{ID: t.ID, Name: t.Name, Kind: t.Kind, AgeRecipient: t.AgeRecipient, CreatedAt: t.CreatedAt}
		}
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
	if in.Encrypt || in.AgeIdentity != "" {
		var err error
		if t.AgeIdentity, t.AgeRecipient, err = c.targetKey(ctx, t, in.AgeIdentity); err != nil {
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

// EncryptBackupTarget gives a target (the local one too) an age key, a new
// one or identity: backups made from now on are encrypted, earlier ones
// stay readable as they are. It can't be turned off: the key must outlive
// the backups it encrypted.
func (c *Core) EncryptBackupTarget(ctx context.Context, id, identity string) (store.BackupTarget, error) {
	t, err := c.store.GetBackupTarget(ctx, id)
	if err != nil {
		return store.BackupTarget{}, err
	}
	if t.Encrypted() {
		return store.BackupTarget{}, fmt.Errorf("%w: this storage is already encrypted", ErrInvalid)
	}
	identity, recipient, err := c.targetKey(ctx, t, identity)
	if err != nil {
		return store.BackupTarget{}, err
	}
	if err := c.store.SetBackupTargetKey(ctx, id, identity, recipient); errors.Is(err, store.ErrConflict) {
		return store.BackupTarget{}, fmt.Errorf("%w: this storage is already encrypted", ErrInvalid)
	} else if err != nil {
		return store.BackupTarget{}, err
	}
	t, err = c.store.GetBackupTarget(ctx, id)
	return maskedTarget(t), err
}

// targetKey returns a new age key, or identity's once it opens the encrypted
// objects already on t: a wrong key would only show at the first restore.
func (c *Core) targetKey(ctx context.Context, t store.BackupTarget, identity string) (string, string, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return newAgeKey()
	}
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return "", "", fmt.Errorf("%w: not an age key (AGE-SECRET-KEY-1…): %v", ErrInvalid, err)
	}
	st, err := c.openStorage(t)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for obj, err := range st.List(ctx, "") {
		if err != nil {
			return "", "", fmt.Errorf("%w: cannot list the storage to check the key: %v", ErrInvalid, err)
		}
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		rc, err := st.Get(ctx, obj.Key)
		if err != nil {
			return "", "", fmt.Errorf("%w: cannot read %s to check the key: %v", ErrInvalid, obj.Key, err)
		}
		// Decrypt unwraps the file key from the header: nothing more is read.
		_, err = age.Decrypt(rc, id)
		rc.Close()
		if err != nil {
			return "", "", fmt.Errorf("%w: this key doesn't open %s, already on this storage: %v", ErrInvalid, obj.Key, err)
		}
		break
	}
	return id.String(), id.Recipient().String(), nil
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

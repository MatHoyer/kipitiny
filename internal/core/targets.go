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
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"accessKey"`
	// SecretKey equal to SecretMask keeps the stored value on update.
	SecretKey string `json:"secretKey"`
	UseSSL    bool   `json:"useSsl"`
}

func (c *Core) ListBackupTargets(ctx context.Context) ([]store.BackupTarget, error) {
	ts, err := c.store.ListBackupTargets(ctx)
	for i := range ts {
		ts[i] = maskedTarget(ts[i])
	}
	return ts, err
}

func (c *Core) CreateBackupTarget(ctx context.Context, in TargetInput) (store.BackupTarget, error) {
	t := store.BackupTarget{Kind: store.BackupTargetS3}
	applyTargetInput(&t, in)
	if err := c.checkTarget(ctx, t); err != nil {
		return store.BackupTarget{}, err
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
	applyTargetInput(&t, in)
	if err := c.checkTarget(ctx, t); err != nil {
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

func applyTargetInput(t *store.BackupTarget, in TargetInput) {
	t.Name = strings.TrimSpace(in.Name)
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
	t.Prefix = strings.Trim(strings.TrimSpace(in.Prefix), "/")
	t.AccessKey = strings.TrimSpace(in.AccessKey)
	if in.SecretKey != SecretMask {
		t.SecretKey = in.SecretKey
	}
	t.UseSSL = in.UseSSL
}

// checkTarget validates fields, then writes and deletes a test object so a
// misconfigured target fails now rather than at 3am.
func (c *Core) checkTarget(ctx context.Context, t store.BackupTarget) error {
	if t.Name == "" || t.Endpoint == "" || t.Bucket == "" || t.AccessKey == "" || t.SecretKey == "" {
		return fmt.Errorf("%w: name, endpoint, bucket and credentials are required", ErrInvalid)
	}
	st, err := storage.Open(t, c.cfg.DataDir)
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

func maskedTarget(t store.BackupTarget) store.BackupTarget {
	if t.SecretKey != "" {
		t.SecretKey = SecretMask
	}
	return t
}

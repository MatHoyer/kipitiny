package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	// Encrypt generates an age key for the target; only honoured on creation.
	Encrypt bool `json:"encrypt"`
	// Config is a drive target's fields (TargetKind.Fields); a secret equal
	// to SecretMask keeps the stored value.
	Config map[string]string `json:"config"`
}

// TargetKind describes a kind of backup target the UI can create.
type TargetKind struct {
	Kind        store.BackupTargetKind `json:"kind"`
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	// Available is false when the manager can't use it (rclone missing).
	Available bool `json:"available"`
	// Help says how to get the credentials; `code` spans are commands.
	Help   string        `json:"help,omitempty"`
	Fields []TargetField `json:"fields"`
}

type TargetField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Multiline   bool   `json:"multiline,omitempty"`
	// obscured values are stored the way rclone expects (rclone obscure).
	obscured bool
}

var driveKinds = []TargetKind{
	{
		Kind:        store.BackupTargetGoogleDrive,
		Label:       "Google Drive",
		Description: "A folder in your Google Drive.",
		Help: "On a computer with a browser and rclone, run `rclone authorize \"drive\"`, sign in, " +
			"and paste the token it prints.",
		Fields: []TargetField{
			{Key: "token", Label: "OAuth token", Placeholder: `{"access_token":…}`, Required: true, Secret: true, Multiline: true},
			{Key: "client_id", Label: "Client ID", Description: "Optional: your own Google OAuth client, as rclone's shared one is rate limited. Authorize with the same one."},
			{Key: "client_secret", Label: "Client secret", Secret: true},
		},
	},
	{
		Kind:        store.BackupTargetProtonDrive,
		Label:       "Proton Drive",
		Description: "A folder in your Proton Drive, end-to-end encrypted.",
		Help:        "Signs in with your Proton account, through rclone's Proton Drive backend.",
		Fields: []TargetField{
			{Key: "username", Label: "Email", Placeholder: "you@proton.me", Required: true},
			{Key: "password", Label: "Password", Required: true, Secret: true, obscured: true},
			{Key: "2fa", Label: "2FA code", Description: "With two-factor sign-in on: the current code, used once to sign in."},
			{Key: "mailbox_password", Label: "Mailbox password", Description: "Only for accounts with two-password mode.", Secret: true, obscured: true},
		},
	},
}

func driveKind(k store.BackupTargetKind) (TargetKind, bool) {
	for _, d := range driveKinds {
		if d.Kind == k {
			return d, true
		}
	}
	return TargetKind{}, false
}

// BackupTargetKinds lists what can be added: S3 always, drives with rclone.
func (c *Core) BackupTargetKinds() []TargetKind {
	kinds := []TargetKind{{
		Kind:        store.BackupTargetS3,
		Label:       "S3-compatible",
		Description: "AWS S3, Cloudflare R2, Backblaze B2, MinIO…",
		Available:   true,
		Fields:      []TargetField{},
	}}
	ok := storage.RcloneAvailable(c.cfg.Rclone)
	for _, d := range driveKinds {
		d.Available = ok
		kinds = append(kinds, d)
	}
	return kinds
}

// openStorage opens a target; options rclone refreshes are saved back.
func (c *Core) openStorage(t store.BackupTarget) (storage.Storage, error) {
	return storage.Open(t, storage.Env{
		DataDir:    c.cfg.DataDir,
		Rclone:     c.cfg.Rclone,
		SaveConfig: c.store.SetBackupTargetConfig,
	})
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
	switch t.Kind {
	case store.BackupTargetS3:
	case store.BackupTargetGoogleDrive, store.BackupTargetProtonDrive:
		return c.applyDriveConfig(ctx, t, in.Config)
	default:
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

// applyDriveConfig merges a drive target's fields into its rclone options.
// Changing a credential drops what rclone saved from the old one (a Proton
// session), so it signs in again.
func (c *Core) applyDriveConfig(ctx context.Context, t *store.BackupTarget, in map[string]string) error {
	kind, _ := driveKind(t.Kind)
	if !storage.RcloneAvailable(c.cfg.Rclone) {
		return fmt.Errorf("%w: rclone isn't installed on the manager", ErrInvalid)
	}
	cfg := maps.Clone(t.Config)
	if cfg == nil {
		cfg = map[string]string{}
	}
	changed := false
	for _, f := range kind.Fields {
		v := strings.TrimSpace(in[f.Key])
		if f.Secret && v == SecretMask {
			continue
		}
		if f.obscured && v != "" {
			o, err := storage.Obscure(ctx, c.cfg.Rclone, v)
			if err != nil {
				return err
			}
			v = o
		}
		if cfg[f.Key] != v {
			changed = true
		}
		if v == "" {
			delete(cfg, f.Key)
		} else {
			cfg[f.Key] = v
		}
	}
	if changed && len(t.Config) > 0 {
		declared := map[string]bool{}
		for _, f := range kind.Fields {
			declared[f.Key] = true
		}
		for k := range cfg {
			if _, fixed := fixedDriveOptions[t.Kind][k]; !declared[k] && !fixed {
				delete(cfg, k)
			}
		}
	}
	for k, v := range fixedDriveOptions[t.Kind] {
		cfg[k] = v
	}
	for _, f := range kind.Fields {
		if f.Required && cfg[f.Key] == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalid, strings.ToLower(f.Label))
		}
	}
	t.Config = cfg
	return nil
}

// fixedDriveOptions are rclone options kipitiny sets itself.
var fixedDriveOptions = map[store.BackupTargetKind]map[string]string{
	store.BackupTargetGoogleDrive: {"scope": "drive"},
}

// checkTarget validates fields, then writes and deletes a test object so a
// misconfigured target fails now rather than at 3am. A drive target keeps
// what rclone saved while signing in, minus the one-time 2FA code.
func (c *Core) checkTarget(ctx context.Context, t *store.BackupTarget) error {
	if t.Name == "" {
		return fmt.Errorf("%w: a name is required", ErrInvalid)
	}
	if t.Kind == store.BackupTargetS3 && (t.Endpoint == "" || t.Bucket == "" || t.AccessKey == "" || t.SecretKey == "") {
		return fmt.Errorf("%w: name, endpoint, bucket and credentials are required", ErrInvalid)
	}
	// Checked under a fresh id: SaveConfig must not write a row mid-edit.
	probe := *t
	probe.ID = ""
	st, err := c.openStorage(probe)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	timeout := 15 * time.Second
	if t.Rclone() {
		timeout = time.Minute // signing in to a drive takes a while
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := st.Check(ctx); err != nil {
		if t.Rclone() {
			return fmt.Errorf("%w: cannot use the drive: %v", ErrInvalid, err)
		}
		return fmt.Errorf("%w: cannot use bucket: %v", ErrInvalid, err)
	}
	if r, ok := st.(*storage.Rclone); ok {
		t.Config = r.Config()
		delete(t.Config, "2fa")
	}
	return nil
}

// maskedTarget hides credentials; a drive target shows its fields as
// Settings, secrets masked and rclone's own options left out.
func maskedTarget(t store.BackupTarget) store.BackupTarget {
	if t.SecretKey != "" {
		t.SecretKey = SecretMask
	}
	if kind, ok := driveKind(t.Kind); ok {
		t.Settings = map[string]string{}
		for _, f := range kind.Fields {
			v := t.Config[f.Key]
			if f.Secret && v != "" {
				v = SecretMask
			}
			if v != "" && f.Key != "2fa" {
				t.Settings[f.Key] = v
			}
		}
	}
	t.Config = nil
	return t
}

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

// sidecarExt names a backup's sidecar: <object key>.json, the backup's
// record encrypted like the backup, so another manager pointed at the same
// storage can adopt it.
const sidecarExt = ".json"

// writeSidecar stores b's record next to it. Manager backups get none: they
// belong to the manager that made them.
func writeSidecar(ctx context.Context, st storage.Storage, target domain.BackupTarget, b domain.Backup) error {
	// What belongs to this manager (IDs, schedule, verification) stays out.
	b.ID, b.ServiceID, b.ProjectID, b.TargetID, b.ScheduleID = "", "", "", "", ""
	b.Verification = domain.Verification{}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	var w io.WriteCloser = nopCloser{&buf}
	if target.Encrypted() {
		if w, err = identity.EncryptTo(&buf, target.AgeRecipient); err != nil {
			return err
		}
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return st.Put(ctx, b.ObjectKey+sidecarExt, &buf)
}

// readSidecar reads the record stored next to a backup, decrypting it with
// ageIdentity when it is encrypted. Its ObjectKey is the one the other manager
// used, relative to its own prefix.
func readSidecar(ctx context.Context, st storage.Storage, key, ageIdentity string) (domain.Backup, error) {
	rc, err := st.Get(ctx, key)
	if err != nil {
		return domain.Backup{}, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return domain.Backup{}, err
	}
	if bytes.HasPrefix(data, []byte("age-encryption.org/")) {
		r, err := identity.DecryptFrom(bytes.NewReader(data), ageIdentity)
		if err != nil {
			return domain.Backup{}, err
		}
		if data, err = io.ReadAll(r); err != nil {
			return domain.Backup{}, fmt.Errorf("decrypt: %w", err)
		}
	}
	var b domain.Backup
	if err := json.Unmarshal(data, &b); err != nil {
		return domain.Backup{}, fmt.Errorf("not a backup record: %w", err)
	}
	return b, nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

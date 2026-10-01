// Package storage writes backup objects to local disk, S3-compatible
// buckets, Google Drive (rclone) or Proton Drive (its official CLI),
// streaming in constant memory where the backend allows.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/MatHoyer/kipitiny/internal/store"
)

type Storage interface {
	// Put stores r under key. On error nothing is left behind.
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// Check verifies the target is reachable and writable.
	Check(ctx context.Context) error
}

// Env is what drivers need besides the target.
type Env struct {
	// DataDir holds local backups and rclone's per-command files.
	DataDir string
	// Rclone and ProtonDrive are the CLIs drive targets use (paths or names
	// on PATH).
	Rclone      string
	ProtonDrive string
	// SaveConfig keeps the options rclone changed on a saved drive target.
	SaveConfig func(ctx context.Context, targetID string, config map[string]string) error
}

// rcloneBackends maps drive targets to rclone backend types.
var rcloneBackends = map[store.BackupTargetKind]string{
	store.BackupTargetGoogleDrive: "drive",
}

// Open returns the storage for a target.
func Open(t store.BackupTarget, env Env) (Storage, error) {
	if backend, ok := rcloneBackends[t.Kind]; ok {
		if !RcloneAvailable(env.Rclone) {
			return nil, errors.New("rclone isn't installed on the manager")
		}
		return &Rclone{
			bin:     env.Rclone,
			work:    filepath.Join(env.DataDir, "rclone"),
			id:      t.ID,
			backend: backend,
			prefix:  t.Prefix,
			save:    env.SaveConfig,
			config:  maps.Clone(t.Config),
		}, nil
	}
	switch t.Kind {
	case store.BackupTargetProtonDrive:
		if !ProtonAvailable(env.ProtonDrive) {
			return nil, errors.New("proton-drive isn't installed on the manager")
		}
		return &ProtonDrive{
			bin:    env.ProtonDrive,
			work:   filepath.Join(env.DataDir, "protondrive"),
			id:     t.ID,
			prefix: t.Prefix,
			save:   env.SaveConfig,
			config: maps.Clone(t.Config),
		}, nil
	case store.BackupTargetLocal:
		return &Local{Root: filepath.Join(env.DataDir, "backups")}, nil
	case store.BackupTargetS3:
		cl, err := minio.New(t.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(t.AccessKey, t.SecretKey, ""),
			Secure: t.UseSSL,
			Region: t.Region,
		})
		if err != nil {
			return nil, err
		}
		return &S3{client: cl, bucket: t.Bucket, prefix: t.Prefix}, nil
	default:
		return nil, fmt.Errorf("unknown target kind %q", t.Kind)
	}
}

// Local stores objects as files. Writes go to a temp file renamed into place,
// so a partial backup never looks complete.
type Local struct {
	Root string
}

func (l *Local) path(key string) (string, error) {
	clean := path.Clean("/" + key)[1:]
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return filepath.Join(l.Root, filepath.FromSlash(clean)), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".partial-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = io.Copy(f, readerWithContext(ctx, r))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) Check(ctx context.Context) error {
	if err := l.Put(ctx, ".check", strings.NewReader("ok")); err != nil {
		return err
	}
	return l.Delete(ctx, ".check")
}

type S3 struct {
	client *minio.Client
	bucket string
	prefix string
}

func (s *S3) key(key string) string {
	return strings.TrimPrefix(path.Join(s.prefix, key), "/")
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader) error {
	// Size -1 streams a multipart upload with bounded memory.
	_, err := s.client.PutObject(ctx, s.bucket, s.key(key), r, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		// 16 MiB parts: the uploader buffers one part at a time.
		PartSize: 16 << 20,
	})
	if err != nil {
		// Aborted multipart uploads leave nothing visible, but be explicit.
		_ = s.client.RemoveObject(context.WithoutCancel(ctx), s.bucket, s.key(key), minio.RemoveObjectOptions{})
	}
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, s.key(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; surface a missing object now rather than mid-restore.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, s.key(key), minio.RemoveObjectOptions{})
}

func (s *S3) Check(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	if err := s.Put(ctx, ".kipitiny-check", strings.NewReader("ok")); err != nil {
		return fmt.Errorf("write test object: %w", err)
	}
	return s.Delete(ctx, ".kipitiny-check")
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func readerWithContext(ctx context.Context, r io.Reader) io.Reader { return &ctxReader{ctx, r} }

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

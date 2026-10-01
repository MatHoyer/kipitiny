package storage

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestS3 runs against a real S3-compatible server when configured, e.g.
//
//	docker run -d -p 19000:9000 -e RUSTFS_ACCESS_KEY=test -e RUSTFS_SECRET_KEY=testsecret rustfs/rustfs
//	KIPITINY_TEST_S3_ENDPOINT=localhost:19000 KIPITINY_TEST_S3_ACCESS_KEY=test \
//	  KIPITINY_TEST_S3_SECRET_KEY=testsecret go test ./internal/storage/
func TestS3(t *testing.T) {
	endpoint := os.Getenv("KIPITINY_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("KIPITINY_TEST_S3_ENDPOINT not set")
	}
	ctx := context.Background()
	target := store.BackupTarget{
		Kind:      store.BackupTargetS3,
		Endpoint:  endpoint,
		Bucket:    "kipitiny-test",
		Prefix:    "pg",
		AccessKey: os.Getenv("KIPITINY_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("KIPITINY_TEST_S3_SECRET_KEY"),
	}
	st, err := Open(target, Env{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s3 := st.(*S3)
	if err := st.Check(ctx); err == nil {
		t.Fatal("check passed on a missing bucket")
	}
	if err := s3.client.MakeBucket(ctx, target.Bucket, minio.MakeBucketOptions{}); err != nil {
		if exists, _ := s3.client.BucketExists(ctx, target.Bucket); !exists {
			t.Fatal(err)
		}
	}
	if err := st.Check(ctx); err != nil {
		t.Fatal(err)
	}

	// Larger than one part to exercise multipart streaming.
	big := strings.Repeat("0123456789abcdef", 1<<21) // 32 MiB
	if err := st.Put(ctx, "shop/db/a.dump", strings.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	info, err := s3.client.StatObject(ctx, target.Bucket, "pg/shop/db/a.dump", minio.StatObjectOptions{})
	if err != nil || info.Size != int64(len(big)) {
		t.Fatalf("object under prefix: %+v %v", info, err)
	}
	rc, err := st.Get(ctx, "shop/db/a.dump")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != big {
		t.Fatal("content mismatch")
	}

	if err := st.Put(ctx, "shop/db/b.dump", &failingReader{n: 3}); err == nil {
		t.Fatal("failed upload reported success")
	}
	if _, err := st.Get(ctx, "shop/db/b.dump"); err == nil {
		t.Fatal("failed upload left an object")
	}

	if err := st.Delete(ctx, "shop/db/a.dump"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "shop/db/a.dump"); err == nil {
		t.Fatal("deleted object still readable")
	}
}

package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/progress"
)

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("boom")
	}
	f.n--
	return copy(p, "data"), nil
}

func TestLocal(t *testing.T) {
	ctx := context.Background()
	l := &Local{Root: t.TempDir()}

	if err := l.Put(ctx, "shop/db/a.dump", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Get(ctx, "shop/db/a.dump")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}

	if err := l.Put(ctx, "shop/db/b.dump", &failingReader{n: 2}); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(filepath.Join(l.Root, "shop", "db"))
	if len(entries) != 1 {
		t.Fatalf("partial file left behind: %v", entries)
	}

	if err := l.Put(ctx, "", strings.NewReader("x")); err == nil {
		t.Error("empty key accepted")
	}
	// Traversal is confined to the root rather than escaping it.
	for key, want := range map[string]string{"../escape": "escape", "/../../etc/passwd": "etc/passwd"} {
		if err := l.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(l.Root, want)); err != nil {
			t.Errorf("key %q not confined to root: %v", key, err)
		}
	}

	if got := listKeys(t, l, ""); !slices.Equal(got, []string{"escape", "etc/passwd", "shop/db/a.dump"}) {
		t.Errorf("list = %q", got)
	}
	if got := listKeys(t, l, "shop/"); !slices.Equal(got, []string{"shop/db/a.dump"}) {
		t.Errorf("list shop/ = %q", got)
	}
	if got := listKeys(t, l, "nowhere/"); len(got) != 0 {
		t.Errorf("list nowhere/ = %q", got)
	}

	if err := l.Delete(ctx, "shop/db/a.dump"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, "shop/db/a.dump"); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	if err := l.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLocalCheckSteps(t *testing.T) {
	l := &Local{Root: t.TempDir()}
	var steps []string
	if err := l.Check(progress.With(context.Background(), func(s string) { steps = append(steps, s) })); err != nil {
		t.Fatal(err)
	}
	if want := []string{StepWrite, StepDelete}; !slices.Equal(steps, want) {
		t.Errorf("steps = %q, want %q", steps, want)
	}
}

func listKeys(t *testing.T, st Storage, prefix string) []string {
	t.Helper()
	var keys []string
	for obj, err := range st.List(context.Background(), prefix) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, obj.Key)
	}
	slices.Sort(keys)
	return keys
}

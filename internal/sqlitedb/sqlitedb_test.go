package sqlitedb

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrunePreMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	for _, v := range []int{2, 9, 10, 11, 12} {
		os.WriteFile(fmt.Sprintf("%s.pre-migrate-%d", path, v), nil, 0o600)
	}
	prunePreMigrate(path)
	left, _ := filepath.Glob(path + ".pre-migrate-*")
	slices.Sort(left)
	want := []string{path + ".pre-migrate-10", path + ".pre-migrate-11", path + ".pre-migrate-12"}
	if !slices.Equal(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

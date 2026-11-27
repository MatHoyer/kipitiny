package docker

import (
	"archive/tar"
	"io"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestTarFiles(t *testing.T) {
	r, err := tarFiles([]domain.ContainerFile{{Path: "/etc/traefik/a.yml", Content: []byte("x")}, {Path: "/etc/traefik/b.yml"}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	// No directory entries: they would reset existing directories.
	want := []string{"etc/traefik/a.yml", "etc/traefik/b.yml"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

package docker

import (
	"archive/tar"
	"io"
	"testing"
)

func TestParseMountinfoID(t *testing.T) {
	id := "3f4e5d6c7b8a99887766554433221100ffeeddccbbaa00112233445566778899"
	in := "1 0 0:1 / / rw - overlay overlay rw\n" +
		"2 1 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw - ext4 /dev/sda1 rw\n"
	if got := parseMountinfoID([]byte(in)); got != id {
		t.Errorf("got %q", got)
	}
	if got := parseMountinfoID([]byte("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n")); got != "" {
		t.Errorf("host process: got %q", got)
	}
}

func TestTarFiles(t *testing.T) {
	r, err := tarFiles([]File{{Path: "/etc/traefik/a.yml", Content: []byte("x")}, {Path: "/etc/traefik/b.yml"}})
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
	want := []string{"etc/", "etc/traefik/", "etc/traefik/a.yml", "etc/traefik/b.yml"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

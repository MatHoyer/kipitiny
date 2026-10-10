package core

import (
	"bufio"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestVolumePath(t *testing.T) {
	app := store.Service{Name: "web", Kind: store.ServiceKindApp, Volumes: []store.Volume{{Name: "data", Path: "/srv/data"}}}
	pg := store.Service{Name: "db", Kind: store.ServiceKindPostgres}
	for _, tc := range []struct {
		svc      store.Service
		in, want string
		err      error
	}{
		{app, "", "", nil},
		{app, "./", "", nil},
		{app, "data", "/v/data", nil},
		{app, "data/mods//a b.jar", "/v/data/mods/a b.jar", nil},
		{app, "data/./mods/", "/v/data/mods", nil},
		{app, "data/../etc", "", ErrInvalid},
		{app, "data/mods/..", "", ErrInvalid},
		{app, "/data", "", ErrInvalid},
		{app, "data/a\x00b", "", ErrInvalid},
		{app, "other/x", "", store.ErrNotFound},
		{pg, "data/base", "/v/data/base", nil},
		{app, "@container", "/", nil},
		{app, "@container/etc/nginx/", "/etc/nginx", nil},
		{pg, "@0123456789ab/var/run", "/var/run", nil},
		{app, "@container/../etc", "", ErrInvalid},
		{app, "@other/x", "", ErrInvalid},
		{app, "@0123/x", "", ErrInvalid},
	} {
		_, got, err := volumePath(tc.svc, tc.in)
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("volumePath(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestFileVolumeDisplay(t *testing.T) {
	vol := fileVolume{name: "data"}
	ct := fileVolume{name: FilesContainer, container: true}
	for _, tc := range []struct {
		v        fileVolume
		abs, out string
	}{
		{vol, "/v/data", "data"},
		{vol, "/v/data/mods/a.jar", "data/mods/a.jar"},
		{ct, "/", "@container"},
		{ct, "/etc/nginx", "@container/etc/nginx"},
	} {
		if got := tc.v.display(tc.abs); got != tc.out {
			t.Errorf("display(%q) = %q, want %q", tc.abs, got, tc.out)
		}
	}
	if ct.root() != "" || ct.top() != "/" || vol.top() != "/v/data" {
		t.Error("roots")
	}
}

func TestFileVolumes(t *testing.T) {
	if v := fileVolumes(store.Service{Kind: store.ServiceKindRedis}); len(v) != 1 || v[0] != (fileVolume{name: "data", mountPath: "/data"}) {
		t.Errorf("redis: %v", v)
	}
	if !filesReadOnly(store.Service{Kind: store.ServiceKindPostgres}) || filesReadOnly(store.Service{Kind: store.ServiceKindApp}) {
		t.Error("only databases are read-only")
	}
}

func TestReadEntries(t *testing.T) {
	out := "41ed 4096 1791631227 0 0\nmods\x00\x00" +
		"81a4 12 1791631228 1000 1000\nnew\nline.txt\x00\x00" +
		"a1ff 4 1791631229 0 0\nlnk\x00/etc\x00"
	var got []VolumeEntry
	if err := readEntries(strings.NewReader(out), func(e VolumeEntry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []VolumeEntry{
		{Name: "mods", Type: "dir", Mode: "0755", Modified: time.Unix(1791631227, 0).UTC()},
		{Name: "new\nline.txt", Type: "file", Size: 12, Mode: "0644", UID: 1000, GID: 1000, Modified: time.Unix(1791631228, 0).UTC()},
		{Name: "lnk", Type: "link", Size: 4, Mode: "0777", Target: "/etc", Modified: time.Unix(1791631229, 0).UTC()},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	if err := readEntries(strings.NewReader("41ed 4096\nx\x00\x00"), func(VolumeEntry) error { return nil }); err == nil {
		t.Error("short line: no error")
	}
	if _, err := readEntry(bufio.NewReader(strings.NewReader("41ed 1 2 3 4\ncut"))); err == nil {
		t.Error("cut record: no error")
	}
}

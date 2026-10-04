package core

import (
	"errors"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestValidateVolumes(t *testing.T) {
	tests := []struct {
		name    string
		vols    []store.Volume
		wantErr bool
	}{
		{"none", nil, false},
		{"two", []store.Volume{{Name: "uploads", Path: "/app/uploads"}, {Name: "cache", Path: "/var/cache/app"}}, false},
		{"uppercase name", []store.Volume{{Name: "Uploads", Path: "/data"}}, true},
		{"trailing dash", []store.Volume{{Name: "up-", Path: "/data"}}, true},
		{"relative path", []store.Volume{{Name: "data", Path: "data"}}, true},
		{"unclean path", []store.Volume{{Name: "data", Path: "/data/"}}, true},
		{"root", []store.Volume{{Name: "data", Path: "/"}}, true},
		{"probe dir", []store.Volume{{Name: "data", Path: probeDir + "/x"}}, true},
		{"colon", []store.Volume{{Name: "data", Path: "/a:b"}}, true},
		{"duplicate name", []store.Volume{{Name: "data", Path: "/a"}, {Name: "data", Path: "/b"}}, true},
		{"duplicate path", []store.Volume{{Name: "a", Path: "/data"}, {Name: "b", Path: "/data"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVolumes(tt.vols)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestDatabaseRefusesVolumes(t *testing.T) {
	svc := store.Service{Kind: store.ServiceKindRedis, Image: DefaultRedisImage, Replicas: 1, MemoryMB: 256,
		Env: newRedisEnv(), Volumes: []store.Volume{{Name: "x", Path: "/x"}}}
	if err := validateService(svc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestAppVolumeMounts(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	svc := store.Service{ID: "01ABC", ProjectID: "P1", Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 2,
		Volumes: []store.Volume{{Name: "uploads", Path: "/app/uploads"}}}
	mounts := containerSpec(p, svc, envSources{}, "D1", 1, "").HostConfig.Mounts
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v", mounts)
	}
	m := mounts[0]
	if m.Source != "kipitiny-vol-01abc-uploads" || m.Target != "/app/uploads" || m.ReadOnly {
		t.Errorf("mount = %+v", m)
	}
	if l := m.VolumeOptions.Labels; l[docker.LabelService] != "01ABC" || l[docker.LabelComponent] != volumeComponent || l[docker.LabelManaged] != "true" {
		t.Errorf("volume labels = %v", l)
	}
	if !hasData(svc) || hasData(store.Service{Kind: store.ServiceKindApp}) {
		t.Error("hasData")
	}
}

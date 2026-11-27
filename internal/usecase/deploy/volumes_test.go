package deploy

import (
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestDatabaseRefusesVolumes(t *testing.T) {
	svc := domain.Service{Kind: domain.ServiceKindRedis, Image: data.DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: data.NewRedisEnv()}
	if err := validateService(svc); err != nil {
		t.Fatal(err)
	}
	withVolume, withPreBackup := svc, svc
	withVolume.Volumes = []domain.Volume{{Name: "x", Path: "/x"}}
	withPreBackup.PreBackup = "redis-cli save"
	for _, s := range []domain.Service{withVolume, withPreBackup} {
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v, want ErrInvalid", err)
		}
	}
}

func TestAppVolumeMounts(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "shop"}
	svc := domain.Service{ID: "01ABC", ProjectID: "P1", Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 2,
		Volumes: []domain.Volume{{Name: "uploads", Path: "/app/uploads"}}}
	mounts := containerSpec(p, svc, envSources{}, "D1", 1, network.Routing{}).Mounts
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v", mounts)
	}
	m := mounts[0]
	if m.Source != "kipitiny-vol-01abc-uploads" || m.Target != "/app/uploads" || m.ReadOnly {
		t.Errorf("mount = %+v", m)
	}
	if l := m.Volume.Labels; l[domain.LabelService] != "01ABC" || l[domain.LabelComponent] != data.VolumeComponent || l[domain.LabelManaged] != "true" {
		t.Errorf("volume labels = %v", l)
	}
	if !data.HasData(svc) || data.HasData(domain.Service{Kind: domain.ServiceKindApp}) {
		t.Error("hasData")
	}
}

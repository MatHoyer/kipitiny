package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestExternalVolumesDocker checks that a missing external volume fails the
// deploy instead of being created empty.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run ExternalVolumesDocker
func TestExternalVolumesDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	const name = "kipitiny-test-external-vol"
	if _, err := dk.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = dk.VolumeRemove(context.Background(), name, client.VolumeRemoveOptions{}) })

	svc := store.Service{Volumes: []store.Volume{{Name: "data", Path: "/data", External: name}}}
	if err := checkExternalVolumes(ctx, dk, svc); err != nil {
		t.Fatalf("existing volume: %v", err)
	}
	svc.Volumes[0].External = "kipitiny-test-missing-vol"
	if err := checkExternalVolumes(ctx, dk, svc); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatalf("missing volume: got %v", err)
	}
	if _, err := dk.VolumeInspect(ctx, "kipitiny-test-missing-vol", client.VolumeInspectOptions{}); err == nil {
		t.Fatal("the check created the missing volume")
	}
}

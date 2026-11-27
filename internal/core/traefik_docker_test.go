package core

import (
	"context"
	"os"
	"testing"

	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// TestTraefikImageUpgradeDocker checks a manager update that changes the
// Traefik image moves a running server onto it: Traefik is pulled only when
// missing, so a floating tag pulled once (an early v3.7) stays forever, and
// only a new image string recreates the container.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run TraefikImageUpgradeDocker
func TestTraefikImageUpgradeDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	// An old server: the floating tag still names the release it first pulled.
	const floating = "kipitiny-test/traefik:v3.7"
	if err := dk.EnsureImage(ctx, "traefik:v3.7.0", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := dk.ImageTag(ctx, client.ImageTagOptions{Source: "traefik:v3.7.0", Target: floating}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = dk.ImageRemove(context.Background(), floating, client.ImageRemoveOptions{}) })

	if err := dk.EnsureNetwork(ctx, domain.ProxyNetwork); err != nil { // the reconciler's job
		t.Fatal(err)
	}

	c := newTestCore(t, config.Config{Addr: ":3000", DataDir: t.TempDir(), Traefik: config.Traefik{
		Image: floating, DockerSocket: "/var/run/docker.sock", HTTPPort: "0", HTTPSPort: "0",
	}})
	c.useDocker(dk)
	sv, err := c.store.GetServer(ctx, domain.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cts, _ := dk.ListContainers(context.Background(), map[string]string{domain.LabelComponent: network.TraefikComponent})
		for _, ct := range cts {
			_ = dk.RemoveContainer(context.Background(), ct.ID, 0)
		}
	})
	running := func() (id, image string) {
		t.Helper()
		cts, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: network.TraefikComponent})
		if err != nil || len(cts) != 1 {
			t.Fatalf("traefik containers: %v %v", cts, err)
		}
		return cts[0].ID, cts[0].ImageID
	}
	imageID := func(ref string) string {
		t.Helper()
		res, err := dk.ImageInspect(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}

	if err := c.EnsureTraefik(ctx, sv); err != nil {
		t.Fatal(err)
	}
	oldID, img := running()
	if img != imageID("traefik:v3.7.0") {
		t.Fatalf("old server runs %s, want v3.7.0's image", img)
	}
	// Reconciling again with the same tag keeps the old build: the bug.
	if err := c.EnsureTraefik(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if id, _ := running(); id != oldID {
		t.Fatal("traefik recreated without a config change")
	}

	// The manager update: the new default names an exact patch release.
	c.cfg.Traefik.Image = config.Load().Traefik.Image
	if err := c.EnsureTraefik(ctx, sv); err != nil {
		t.Fatal(err)
	}
	newID, img := running()
	if newID == oldID {
		t.Fatal("traefik not recreated after the image changed")
	}
	if want := imageID(c.cfg.Traefik.Image); img != want {
		t.Fatalf("traefik runs %s, want %s (%s)", img, want, c.cfg.Traefik.Image)
	}
}

package core

import (
	"context"
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestSetServiceNetworksValidation(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.store.CreateServer(ctx, domain.Server{Name: "other", Kind: domain.ServerSSH, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := c.store.CreateNetwork(ctx, domain.Network{ServerID: other.ID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.SetServiceNetworks(ctx, svc.ID, []string{"nope"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown network: got %v, want ErrInvalid", err)
	}
	if _, err := c.SetServiceNetworks(ctx, svc.ID, []string{remote.ID}); !errors.Is(err, ErrInvalid) {
		t.Errorf("network on another server: got %v, want ErrInvalid", err)
	}

	local, err := c.store.CreateNetwork(ctx, domain.Network{ServerID: domain.LocalServerID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	svc.HostNetwork = true
	if _, err := c.store.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetServiceNetworks(ctx, svc.ID, []string{local.ID}); !errors.Is(err, ErrInvalid) {
		t.Errorf("host network: got %v, want ErrInvalid", err)
	}
	if got, _ := c.store.GetService(ctx, svc.ID); len(got.Networks) != 0 {
		t.Errorf("refused change was saved: %v", got.Networks)
	}
}

func TestCreateNetworkName(t *testing.T) {
	c := newTestCore(t, config.Config{})
	for _, name := range []string{"", "Shared", "a_b", "-x"} {
		if _, err := c.CreateNetwork(context.Background(), NetworkInput{Name: name}); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: got %v, want ErrInvalid", name, err)
		}
	}
	if _, err := c.CreateNetwork(context.Background(), NetworkInput{Name: "shared", ServerID: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown server: got %v, want ErrInvalid", err)
	}
}

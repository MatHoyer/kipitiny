package core

import (
	"maps"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestNetworkIssues(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	svc := store.Service{ID: "S1", Name: "web", ProjectID: p.ID, CurrentDeploymentID: "D1", Networks: []string{"N1"}}
	ct := func(state container.ContainerState, routed bool, nets ...string) container.Summary {
		c := container.Summary{
			State:           state,
			Labels:          map[string]string{docker.LabelDeploy: "D1"},
			NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{}},
		}
		if routed {
			c.Labels["traefik.enable"] = "true"
		}
		for _, n := range nets {
			c.NetworkSettings.Networks[n] = &network.EndpointSettings{}
		}
		return c
	}
	all := []string{docker.ProjectNetwork(p.ID), docker.CustomNetwork("N1"), docker.ProxyNetwork}

	want := wantNetworks(p, svc, ct(container.StateRunning, true))
	if got := slices.Sorted(maps.Keys(want)); !slices.Equal(got, slices.Sorted(slices.Values(all))) {
		t.Errorf("wanted networks = %q", got)
	}
	if aliases := want[docker.ProjectNetwork(p.ID)]; !slices.Equal(aliases, []string{"web"}) {
		t.Errorf("project alias = %q", aliases)
	}

	if got := networkIssues(p, svc, []container.Summary{ct(container.StateRunning, true, all...)}); len(got) != 0 {
		t.Errorf("attached everywhere: %q", got)
	}
	// The project network was removed and recreated: the replicas lost it.
	got := networkIssues(p, svc, []container.Summary{
		ct(container.StateRunning, true, docker.CustomNetwork("N1"), docker.ProxyNetwork),
		ct(container.StateRunning, true, docker.CustomNetwork("N1")),
	})
	if want := []string{"not on the project network: other services can't reach it", "not on the proxy network: its domain can't reach it"}; !slices.Equal(got, want) {
		t.Errorf("detached: %q, want %q", got, want)
	}
	// A stopped one, or one of another deployment, isn't counted.
	if got := networkIssues(p, svc, []container.Summary{ct(container.StateExited, false)}); len(got) != 0 {
		t.Errorf("stopped: %q", got)
	}
	if got := networkIssues(p, store.Service{HostNetwork: true, CurrentDeploymentID: "D1"}, []container.Summary{ct(container.StateRunning, false)}); len(got) != 0 {
		t.Errorf("host network: %q", got)
	}
}

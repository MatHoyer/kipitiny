package deploy

import (
	"maps"
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestNetworkIssues(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "shop"}
	svc := domain.Service{ID: "S1", Name: "web", ProjectID: p.ID, CurrentDeploymentID: "D1", Networks: []string{"N1"}}
	ct := func(state string, routed bool, nets ...string) domain.Container {
		c := domain.Container{
			State:    state,
			Labels:   map[string]string{domain.LabelDeploy: "D1"},
			Networks: map[string]domain.ContainerEndpoint{},
		}
		if routed {
			c.Labels["traefik.enable"] = "true"
		}
		for _, n := range nets {
			c.Networks[n] = domain.ContainerEndpoint{}
		}
		return c
	}
	all := []string{domain.ProjectNetwork(p.ID), domain.CustomNetwork("N1"), domain.ProxyNetwork}

	want := wantNetworks(p, svc, ct(domain.StateRunning, true))
	if got := slices.Sorted(maps.Keys(want)); !slices.Equal(got, slices.Sorted(slices.Values(all))) {
		t.Errorf("wanted networks = %q", got)
	}
	if aliases := want[domain.ProjectNetwork(p.ID)]; !slices.Equal(aliases, []string{"web"}) {
		t.Errorf("project alias = %q", aliases)
	}

	if got := networkIssues(p, svc, []domain.Container{ct(domain.StateRunning, true, all...)}); len(got) != 0 {
		t.Errorf("attached everywhere: %q", got)
	}
	// The project network was removed and recreated: the replicas lost it.
	got := networkIssues(p, svc, []domain.Container{
		ct(domain.StateRunning, true, domain.CustomNetwork("N1"), domain.ProxyNetwork),
		ct(domain.StateRunning, true, domain.CustomNetwork("N1")),
	})
	if want := []string{"not on the project network: other services can't reach it", "not on the proxy network: its domain can't reach it"}; !slices.Equal(got, want) {
		t.Errorf("detached: %q, want %q", got, want)
	}
	// A stopped one, or one of another deployment, isn't counted.
	if got := networkIssues(p, svc, []domain.Container{ct(domain.StateExited, false)}); len(got) != 0 {
		t.Errorf("stopped: %q", got)
	}
	if got := networkIssues(p, domain.Service{HostNetwork: true, CurrentDeploymentID: "D1"}, []domain.Container{ct(domain.StateRunning, false)}); len(got) != 0 {
		t.Errorf("host network: %q", got)
	}
}

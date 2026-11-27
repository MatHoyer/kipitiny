package deploy

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func readinessMode(svc domain.Service) string {
	switch {
	case svc.Runtime.Restart == "no":
		return "a job: exiting with code 0"
	case svc.Healthcheck.Set() && svc.Healthcheck.Test[0] == "NONE":
		return "running for 5s"
	case svc.Healthcheck.Set():
		return "its healthcheck passing"
	case svc.HealthPath != "":
		return fmt.Sprintf("HTTP GET :%d%s", svc.Port, svc.HealthPath)
	case network.ProbePort(svc) > 0:
		return fmt.Sprintf("port %d accepting connections", network.ProbePort(svc))
	}
	return "running for 5s"
}

// containerSpec builds the container for one replica. src resolves env
// references.
func containerSpec(project domain.Project, svc domain.Service, src envSources, deployID string, replica int, rt network.Routing) domain.ContainerSpec {
	labels := map[string]string{
		domain.LabelManaged: "true",
		domain.LabelProject: project.ID,
		domain.LabelService: svc.ID,
		domain.LabelReplica: strconv.Itoa(replica),
		domain.LabelDeploy:  deployID,
	}
	endpoints := map[string][]string{}
	for _, n := range projectNetworks(project, svc) {
		// Other services of the project reach this one by its service name.
		endpoints[n] = []string{svc.Name}
	}
	for _, id := range svc.Networks {
		endpoints[domain.CustomNetwork(id)] = []string{networkAlias(project, svc)}
	}
	if svc.HostNetwork {
		endpoints = map[string][]string{}
	}
	if network.HTTPRouted(svc) {
		maps.Copy(labels, network.TraefikLabels(svc, rt))
		endpoints[domain.ProxyNetwork] = nil
	}

	envMap := resolveEnv(svc.Env, src)
	if src.errors != nil {
		inject := maps.Clone(src.errors)
		inject["SENTRY_RELEASE"] = monitoring.ImageRelease(svc.Image)
		for k, v := range inject {
			if _, own := envMap[k]; !own {
				envMap[k] = v
			}
		}
	}
	env := make([]string, 0, len(envMap))
	for _, k := range slices.Sorted(maps.Keys(envMap)) {
		env = append(env, k+"="+envMap[k])
	}

	spec := domain.ContainerSpec{
		Image:    svc.Image,
		Env:      env,
		Labels:   labels,
		Restart:  domain.RestartPolicy{Name: domain.RestartUnlessStopped},
		Log:      true,
		Networks: endpoints,
		Platform: domain.ParsePlatform(svc.Runtime.Platform),
		// No raw sockets: a compromised container can't ARP-spoof its
		// networks (e.g. the plain-HTTP Traefik → manager hop). Ping still
		// works through Docker's default ping_group_range.
		CapDrop: []string{"NET_RAW"},
	}
	if svc.MemoryMB > 0 {
		spec.Memory = int64(svc.MemoryMB) << 20
	}
	if svc.CPUs > 0 {
		spec.NanoCPUs = int64(math.Round(svc.CPUs * 1e9))
	}
	if svc.Kind.IsDatabase() {
		data.ApplyDatabaseSpec(&spec, svc)
	} else {
		spec.Mounts = append(spec.Mounts, data.AppVolumeMounts(svc)...)
		if svc.HostNetwork {
			spec.NetworkMode = "host"
		}
		if svc.DockerSocket != "" && rt.DockerSocket != "" {
			spec.Mounts = append(spec.Mounts, domain.Mount{
				Type: domain.MountBind, Source: rt.DockerSocket, Target: compose.DockerSocket, ReadOnly: svc.DockerSocket == "ro",
			})
		}
		spec.ExposedPorts, spec.PortBindings = network.PortBindings(svc.PublishedPorts)
		if svc.Healthcheck.Set() {
			spec.Healthcheck = healthConfig(svc.Healthcheck)
		}
		if svc.StopGraceSeconds > 0 {
			// Docker's own stops (daemon shutdown, restarts) wait as long.
			spec.StopTimeout = &svc.StopGraceSeconds
		}
		applyRuntime(&spec, svc.Runtime)
	}
	spec.Name = fmt.Sprintf("%s-%s-%d", project.Name, svc.Name, replica)
	if !svc.Kind.IsDatabase() {
		// Old and new replicas run side by side during a rollout.
		spec.Name += "-" + deploySuffix(deployID)
	}
	return spec
}

// deploySuffix is a short, name-safe token from a deployment ID.
func deploySuffix(deployID string) string {
	id := strings.ToLower(deployID)
	return id[max(0, len(id)-6):]
}

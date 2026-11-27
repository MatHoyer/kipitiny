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

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockernet "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/docker"
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
func containerSpec(project domain.Project, svc domain.Service, src envSources, deployID string, replica int, rt network.Routing) client.ContainerCreateOptions {
	labels := map[string]string{
		domain.LabelManaged: "true",
		domain.LabelProject: project.ID,
		domain.LabelService: svc.ID,
		domain.LabelReplica: strconv.Itoa(replica),
		domain.LabelDeploy:  deployID,
	}
	endpoints := map[string]*dockernet.EndpointSettings{}
	for _, n := range projectNetworks(project, svc) {
		// Other services of the project reach this one by its service name.
		endpoints[n] = &dockernet.EndpointSettings{Aliases: []string{svc.Name}}
	}
	for _, id := range svc.Networks {
		endpoints[domain.CustomNetwork(id)] = &dockernet.EndpointSettings{Aliases: []string{networkAlias(project, svc)}}
	}
	if svc.HostNetwork {
		endpoints = map[string]*dockernet.EndpointSettings{}
	}
	if network.HTTPRouted(svc) {
		maps.Copy(labels, network.TraefikLabels(svc, rt))
		endpoints[domain.ProxyNetwork] = &dockernet.EndpointSettings{}
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

	cfg := &container.Config{
		Image:  svc.Image,
		Env:    env,
		Labels: labels,
	}
	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		LogConfig:     docker.DefaultLogConfig(),
		// No raw sockets: a compromised container can't ARP-spoof its
		// networks (e.g. the plain-HTTP Traefik → manager hop). Ping still
		// works through Docker's default ping_group_range.
		CapDrop: []string{"NET_RAW"},
	}
	if svc.MemoryMB > 0 {
		host.Memory = int64(svc.MemoryMB) << 20
	}
	if svc.CPUs > 0 {
		host.NanoCPUs = int64(math.Round(svc.CPUs * 1e9))
	}
	if svc.Kind.IsDatabase() {
		data.ApplyDatabaseSpec(cfg, host, svc)
	} else {
		host.Mounts = append(host.Mounts, data.AppVolumeMounts(svc)...)
		if svc.HostNetwork {
			host.NetworkMode = "host"
		}
		if svc.DockerSocket != "" && rt.DockerSocket != "" {
			host.Mounts = append(host.Mounts, mount.Mount{
				Type: mount.TypeBind, Source: rt.DockerSocket, Target: compose.DockerSocket, ReadOnly: svc.DockerSocket == "ro",
			})
		}
		cfg.ExposedPorts, host.PortBindings = network.PortBindings(svc.PublishedPorts)
		if svc.Healthcheck.Set() {
			cfg.Healthcheck = healthConfig(svc.Healthcheck)
		}
		if svc.StopGraceSeconds > 0 {
			// Docker's own stops (daemon shutdown, restarts) wait as long.
			cfg.StopTimeout = &svc.StopGraceSeconds
		}
		applyRuntime(cfg, host, svc.Runtime)
	}
	name := fmt.Sprintf("%s-%s-%d", project.Name, svc.Name, replica)
	if !svc.Kind.IsDatabase() {
		// Old and new replicas run side by side during a rollout.
		name += "-" + deploySuffix(deployID)
	}
	return client.ContainerCreateOptions{
		Name:             name,
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: &dockernet.NetworkingConfig{EndpointsConfig: endpoints},
		Platform:         docker.Platform(svc.Runtime.Platform),
	}
}

// deploySuffix is a short, name-safe token from a deployment ID.
func deploySuffix(deployID string) string {
	id := strings.ToLower(deployID)
	return id[max(0, len(id)-6):]
}

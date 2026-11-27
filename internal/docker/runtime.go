package docker

import (
	"context"
	"encoding/json"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Client is the use cases' container runtime.
var _ domain.ContainerRuntime = (*Client)(nil)

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Client.Ping(ctx, client.PingOptions{})
	return err
}

func (c *Client) Events(ctx context.Context, f domain.Filter, fn func(domain.ContainerEvent)) error {
	res := c.Client.Events(ctx, client.EventsListOptions{Filters: filters(f)})
	for {
		select {
		case m := <-res.Messages:
			fn(domain.ContainerEvent{Action: string(m.Action), Attributes: m.Actor.Attributes})
		case err := <-res.Err:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) ListContainers(ctx context.Context, labels map[string]string) ([]domain.Container, error) {
	return c.FindContainers(ctx, true, domain.LabelFilter(labels))
}

func (c *Client) FindContainers(ctx context.Context, all bool, f domain.Filter) ([]domain.Container, error) {
	res, err := c.ContainerList(ctx, client.ContainerListOptions{All: all, Filters: filters(f)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Container, len(res.Items))
	for i, s := range res.Items {
		out[i] = toContainer(s)
	}
	return out, nil
}

func (c *Client) InspectContainer(ctx context.Context, id string) (domain.ContainerInfo, error) {
	res, err := c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return domain.ContainerInfo{}, err
	}
	return toContainerInfo(res.Container), nil
}

func (c *Client) CreateContainer(ctx context.Context, spec domain.ContainerSpec) (string, error) {
	res, err := c.ContainerCreate(ctx, createOptions(spec))
	return res.ID, err
}

func (c *Client) Run(ctx context.Context, spec domain.ContainerSpec, files ...domain.ContainerFile) (string, error) {
	return c.RunMoby(ctx, createOptions(spec), files...)
}

func (c *Client) RunAttached(ctx context.Context, spec domain.ContainerSpec, stdin io.Reader, out io.Writer) (int, error) {
	return c.RunAttachedMoby(ctx, createOptions(spec), stdin, out)
}

func (c *Client) RunTTY(ctx context.Context, spec domain.ContainerSpec, cols, rows uint) (domain.TTY, error) {
	t, err := c.RunTTYMoby(ctx, createOptions(spec), cols, rows)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, err := c.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout *int) error {
	_, err := c.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: timeout})
	return err
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout *int) error {
	_, err := c.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: timeout})
	return err
}

func (c *Client) WaitContainer(ctx context.Context, id string) (int64, error) {
	wait := c.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-wait.Result:
		return res.StatusCode, nil
	case err := <-wait.Error:
		return 0, err
	}
}

func (c *Client) CopyToContainer(ctx context.Context, id, dir string, archive io.Reader) error {
	_, err := c.Client.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: dir, Content: archive})
	return err
}

func (c *Client) Logs(ctx context.Context, id string, opts domain.LogOptions) (io.ReadCloser, error) {
	rc, err := c.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: opts.Tail, Since: opts.Since, Follow: opts.Follow, Timestamps: opts.Timestamps,
	})
	if err != nil {
		return nil, err
	}
	// Docker multiplexes stdout/stderr (no TTY); demux into one stream.
	pr, pw := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, rc)
		pw.CloseWithError(err)
	}()
	return logStream{pr, rc}, nil
}

// logStream closes the daemon's stream along with the demuxed one.
type logStream struct {
	*io.PipeReader
	rc io.Closer
}

func (l logStream) Close() error {
	l.PipeReader.Close()
	return l.rc.Close()
}

func (c *Client) Stats(ctx context.Context, id string) (domain.ContainerStats, error) {
	res, err := c.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return domain.ContainerStats{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return domain.ContainerStats{}, err
	}
	out := domain.ContainerStats{
		Read: st.Read, CPU: st.CPUStats.CPUUsage.TotalUsage, MemoryUsage: st.MemoryStats.Usage, MemoryStats: st.MemoryStats.Stats,
	}
	for _, n := range st.Networks {
		out.RxBytes += n.RxBytes
		out.TxBytes += n.TxBytes
	}
	return out, nil
}

func (c *Client) PruneContainers(ctx context.Context, f domain.Filter) (domain.PruneReport, error) {
	res, err := c.ContainerPrune(ctx, client.ContainerPruneOptions{Filters: filters(f)})
	return domain.PruneReport{Deleted: len(res.Report.ContainersDeleted), Reclaimed: res.Report.SpaceReclaimed}, err
}

func (c *Client) InspectImage(ctx context.Context, ref, platform string) (domain.Image, error) {
	res, err := c.InspectImageMoby(ctx, ref, platform)
	if err != nil {
		return domain.Image{}, err
	}
	img := domain.Image{ID: res.ID, RepoDigests: res.RepoDigests}
	if cfg := res.Config; cfg != nil {
		img.Labels = cfg.Labels
		if cfg.Healthcheck != nil {
			img.Healthcheck = cfg.Healthcheck.Test
		}
	}
	return img, nil
}

func (c *Client) ListImages(ctx context.Context) ([]domain.Image, error) {
	res, err := c.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Image, len(res.Items))
	for i, img := range res.Items {
		out[i] = domain.Image{ID: img.ID, RepoDigests: img.RepoDigests, Labels: img.Labels, Created: img.Created, Size: img.Size}
	}
	return out, nil
}

func (c *Client) RemoveImage(ctx context.Context, id string) error {
	_, err := c.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	return err
}

func (c *Client) TagImage(ctx context.Context, source, target string) error {
	_, err := c.ImageTag(ctx, client.ImageTagOptions{Source: source, Target: target})
	return err
}

func (c *Client) PruneImages(ctx context.Context, f domain.Filter) (domain.PruneReport, error) {
	res, err := c.ImagePrune(ctx, client.ImagePruneOptions{Filters: filters(f)})
	return domain.PruneReport{Deleted: len(res.Report.ImagesDeleted), Reclaimed: res.Report.SpaceReclaimed}, err
}

func (c *Client) PruneBuildCache(ctx context.Context, f domain.Filter) (domain.PruneReport, error) {
	res, err := c.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true, Filters: filters(f)})
	return domain.PruneReport{Deleted: len(res.Report.CachesDeleted), Reclaimed: res.Report.SpaceReclaimed}, err
}

func (c *Client) ImagePlatforms(ctx context.Context, ref, auth string) ([]domain.Platform, error) {
	res, err := c.DistributionInspect(ctx, ref, client.DistributionInspectOptions{EncodedRegistryAuth: auth})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Platform, len(res.Platforms))
	for i, p := range res.Platforms {
		out[i] = domain.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
	}
	return out, nil
}

func (c *Client) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	_, err := c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels})
	return err
}

func (c *Client) InspectVolume(ctx context.Context, name string) (domain.DockerVolume, error) {
	res, err := c.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return domain.DockerVolume{}, err
	}
	v := res.Volume
	out := domain.DockerVolume{Name: v.Name, Labels: v.Labels, CreatedAt: v.CreatedAt, Size: -1}
	if v.UsageData != nil {
		out.Size = v.UsageData.Size
	}
	return out, nil
}

func (c *Client) ListVolumes(ctx context.Context, f domain.Filter) ([]domain.DockerVolume, error) {
	res, err := c.VolumeList(ctx, client.VolumeListOptions{Filters: filters(f)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.DockerVolume, len(res.Items))
	for i, v := range res.Items {
		out[i] = domain.DockerVolume{Name: v.Name, Labels: v.Labels, CreatedAt: v.CreatedAt, Size: -1}
		if v.UsageData != nil {
			out[i].Size = v.UsageData.Size
		}
	}
	return out, nil
}

func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	_, err := c.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return err
}

func (c *Client) ListNetworks(ctx context.Context) ([]domain.DockerNetwork, error) {
	ns, err := c.ListNetworksMoby(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DockerNetwork, len(ns))
	for i, n := range ns {
		out[i] = domain.DockerNetwork{Name: n.Name}
		for _, cfg := range n.IPAM.Config {
			out[i].Subnets = append(out[i].Subnets, domain.Subnet{Subnet: prefixString(cfg.Subnet.String(), cfg.Subnet.IsValid()), Gateway: prefixString(cfg.Gateway.String(), cfg.Gateway.IsValid())})
		}
	}
	return out, nil
}

// prefixString is "" for an unset address, as Docker leaves them.
func prefixString(s string, valid bool) string {
	if !valid {
		return ""
	}
	return s
}

func (c *Client) PruneNetworks(ctx context.Context, f domain.Filter) (domain.PruneReport, error) {
	res, err := c.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: filters(f)})
	return domain.PruneReport{Deleted: len(res.Report.NetworksDeleted)}, err
}

func filters(f domain.Filter) client.Filters {
	out := make(client.Filters)
	for k, vs := range f {
		out.Add(k, vs...)
	}
	return out
}

func toContainer(s container.Summary) domain.Container {
	ct := domain.Container{ID: s.ID, Names: s.Names, Image: s.Image, ImageID: s.ImageID, Labels: s.Labels, State: string(s.State), Status: s.Status}
	if s.Health != nil {
		ct.Health = string(s.Health.Status)
	}
	if s.NetworkSettings != nil {
		ct.Networks = make(map[string]domain.ContainerEndpoint, len(s.NetworkSettings.Networks))
		for name, ep := range s.NetworkSettings.Networks {
			if ep != nil {
				ct.Networks[name] = domain.ContainerEndpoint{IP: ep.IPAddress, Aliases: ep.Aliases}
			} else {
				ct.Networks[name] = domain.ContainerEndpoint{}
			}
		}
	}
	return ct
}

func toContainerInfo(in container.InspectResponse) domain.ContainerInfo {
	info := domain.ContainerInfo{ID: in.ID, Name: in.Name, RestartCount: in.RestartCount}
	if in.Config != nil {
		info.Image, info.Labels = in.Config.Image, in.Config.Labels
	}
	if st := in.State; st != nil {
		info.State = &domain.ContainerState{
			Status: string(st.Status), Running: st.Running, Restarting: st.Restarting, ExitCode: st.ExitCode, OOMKilled: st.OOMKilled,
		}
		if st.Health != nil {
			info.State.Health = string(st.Health.Status)
		}
	}
	return info
}

// createOptions is the Engine API request creating spec.
func createOptions(s domain.ContainerSpec) client.ContainerCreateOptions {
	cfg := &container.Config{
		Image: s.Image, Cmd: s.Cmd, Entrypoint: s.Entrypoint, Env: s.Env, User: s.User, WorkingDir: s.WorkingDir,
		Labels: s.Labels, StopSignal: s.StopSignal, StopTimeout: s.StopTimeout, Tty: s.Tty, OpenStdin: s.OpenStdin,
	}
	if h := s.Healthcheck; h != nil {
		cfg.Healthcheck = &container.HealthConfig{
			Test: h.Test, Interval: h.Interval, Timeout: h.Timeout, StartPeriod: h.StartPeriod, StartInterval: h.StartInterval, Retries: h.Retries,
		}
	}
	host := &container.HostConfig{
		NetworkMode: container.NetworkMode(s.NetworkMode), ShmSize: s.ShmSize, Tmpfs: s.Tmpfs, ReadonlyRootfs: s.ReadonlyRootfs,
		Init: s.Init, Privileged: s.Privileged, PidMode: container.PidMode(s.PidMode), CapAdd: s.CapAdd, CapDrop: s.CapDrop,
		SecurityOpt: s.SecurityOpt, ExtraHosts: s.ExtraHosts, Binds: s.Binds, Sysctls: s.Sysctls,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(s.Restart.Name), MaximumRetryCount: s.Restart.MaxRetries},
	}
	if s.Log {
		host.LogConfig = DefaultLogConfig()
	}
	host.Memory, host.MemoryReservation, host.NanoCPUs = s.Memory, s.MemoryReservation, s.NanoCPUs
	for _, u := range s.Ulimits {
		host.Ulimits = append(host.Ulimits, &container.Ulimit{Name: u.Name, Soft: u.Soft, Hard: u.Hard})
	}
	for _, d := range s.Devices {
		host.Devices = append(host.Devices, container.DeviceMapping{PathOnHost: d.PathOnHost, PathInContainer: d.PathInContainer, CgroupPermissions: d.CgroupPermissions})
	}
	for _, m := range s.Mounts {
		host.Mounts = append(host.Mounts, toMount(m))
	}
	for _, p := range s.ExposedPorts {
		if port, err := network.ParsePort(p); err == nil {
			if cfg.ExposedPorts == nil {
				cfg.ExposedPorts = network.PortSet{}
			}
			cfg.ExposedPorts[port] = struct{}{}
		}
	}
	for p, bs := range s.PortBindings {
		port, err := network.ParsePort(p)
		if err != nil {
			continue
		}
		if host.PortBindings == nil {
			host.PortBindings = network.PortMap{}
		}
		for _, b := range bs {
			host.PortBindings[port] = append(host.PortBindings[port], network.PortBinding{HostIP: b.HostIP, HostPort: b.HostPort})
		}
	}
	opts := client.ContainerCreateOptions{Name: s.Name, Config: cfg, HostConfig: host}
	if s.Networks != nil {
		eps := map[string]*network.EndpointSettings{}
		for name, aliases := range s.Networks {
			eps[name] = &network.EndpointSettings{Aliases: aliases}
		}
		opts.NetworkingConfig = &network.NetworkingConfig{EndpointsConfig: eps}
	}
	if p := s.Platform; p != nil {
		opts.Platform = &ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
	}
	return opts
}

func toMount(m domain.Mount) mount.Mount {
	out := mount.Mount{Type: mount.Type(m.Type), Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly}
	if v := m.Volume; v != nil {
		out.VolumeOptions = &mount.VolumeOptions{Labels: v.Labels}
		if d := v.Driver; d != nil {
			out.VolumeOptions.DriverConfig = &mount.Driver{Name: d.Name, Options: d.Options}
		}
	}
	return out
}

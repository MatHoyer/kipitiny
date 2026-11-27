package domain

import (
	"context"
	"io"
	"time"
)

// ContainerRuntime is a server's Docker daemon, as the use cases drive it:
// the manager's own server or a remote one over SSH. Errors are the
// implementation's, unwrapped (cerrdefs.IsNotFound and IsConflict tell them
// apart), except where a method says it ignores one.
type ContainerRuntime interface {
	// Info is the daemon's version and platform.
	Info(ctx context.Context) (DockerInfo, error)
	Ping(ctx context.Context) error
	// Events calls fn with each container event f matches until the stream
	// breaks (its error) or ctx ends (ctx's).
	Events(ctx context.Context, f Filter, fn func(ContainerEvent)) error
	// Login checks a registry credential: the daemon signs in.
	Login(ctx context.Context, server, username, password string) error

	// ListContainers returns all containers (running or not) matching every
	// label.
	ListContainers(ctx context.Context, labels map[string]string) ([]Container, error)
	// FindContainers returns the containers f matches, stopped ones too
	// with all.
	FindContainers(ctx context.Context, all bool, f Filter) ([]Container, error)
	InspectContainer(ctx context.Context, id string) (ContainerInfo, error)
	// CreateContainer creates a container without starting it.
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	// Run creates and starts a container, first writing files into it. If
	// anything fails the container is removed.
	Run(ctx context.Context, spec ContainerSpec, files ...ContainerFile) (string, error)
	// RunAttached creates and starts a one-off container, streams stdin
	// into it and its output to out, waits for it to exit and removes it.
	// It returns the exit code.
	RunAttached(ctx context.Context, spec ContainerSpec, stdin io.Reader, out io.Writer) (int, error)
	// RunTTY creates and starts a one-off container on a pseudo-terminal.
	// Close removes it.
	RunTTY(ctx context.Context, spec ContainerSpec, cols, rows uint) (TTY, error)
	StartContainer(ctx context.Context, id string) error
	// StopContainer sends the stop signal, then kills after timeout
	// seconds (nil: the container's or Docker's default).
	StopContainer(ctx context.Context, id string, timeout *int) error
	RestartContainer(ctx context.Context, id string, timeout *int) error
	// WaitContainer waits until the container isn't running and returns its
	// exit code.
	WaitContainer(ctx context.Context, id string) (int64, error)
	// WaitHealthy polls a container until its healthcheck passes.
	// Containers without a healthcheck count as healthy once running.
	WaitHealthy(ctx context.Context, id string, timeout time.Duration) error
	// RemoveContainer stops a container gracefully (SIGTERM, then SIGKILL
	// after timeout) and removes it. Missing containers are not an error.
	RemoveContainer(ctx context.Context, id string, timeout time.Duration) error
	// RemoveContainerAndVolumes force-removes a container with its
	// anonymous volumes. Missing containers are not an error.
	RemoveContainerAndVolumes(ctx context.Context, id string) error
	// RenameContainer ignores containers that are already gone.
	RenameContainer(ctx context.Context, id, name string) error
	// CopyToContainer extracts a tar archive into dir.
	CopyToContainer(ctx context.Context, id, dir string, archive io.Reader) error
	// Logs streams a container's stdout and stderr, merged.
	Logs(ctx context.Context, id string, opts LogOptions) (io.ReadCloser, error)
	// Stats takes a single reading, without the daemon's one-second wait
	// for a previous one.
	Stats(ctx context.Context, id string) (ContainerStats, error)
	// Exec runs a command in a running container, streaming stdin/stdout in
	// constant memory. A non-zero exit yields *ExecError with the stderr
	// tail. Cancelling ctx stops the transfer.
	Exec(ctx context.Context, containerID string, opts ExecOptions) error
	// ExecTTY starts cmd in a running container on a pseudo-terminal.
	ExecTTY(ctx context.Context, containerID string, cmd, env []string, cols, rows uint) (TTY, error)
	PruneContainers(ctx context.Context, f Filter) (PruneReport, error)

	// PullImage pulls ref with auth (RegistryAuth, or "" for none) for
	// platform ("" is the server's), writing one line per status change to
	// w.
	PullImage(ctx context.Context, ref, auth, platform string, w io.Writer) error
	// EnsureImage pulls ref only if it is not present.
	EnsureImage(ctx context.Context, ref, auth, platform string) error
	// InspectImage inspects platform's image of ref ("" is the server's).
	InspectImage(ctx context.Context, ref, platform string) (Image, error)
	ListImages(ctx context.Context) ([]Image, error)
	// RemoveImage removes an image and its untagged parents, without force.
	RemoveImage(ctx context.Context, id string) error
	TagImage(ctx context.Context, source, target string) error
	PruneImages(ctx context.Context, f Filter) (PruneReport, error)
	PruneBuildCache(ctx context.Context, f Filter) (PruneReport, error)
	// ImagePlatforms lists the platforms a registry has ref for, with auth
	// (RegistryAuth).
	ImagePlatforms(ctx context.Context, ref, auth string) ([]Platform, error)

	CreateVolume(ctx context.Context, name string, labels map[string]string) error
	InspectVolume(ctx context.Context, name string) (DockerVolume, error)
	ListVolumes(ctx context.Context, f Filter) ([]DockerVolume, error)
	// RemoveVolume ignores volumes that are already gone.
	RemoveVolume(ctx context.Context, name string) error
	// DeleteVolume removes a volume, gone ones included in the errors.
	DeleteVolume(ctx context.Context, name string) error

	// EnsureNetwork creates a bridge network if it does not exist yet.
	EnsureNetwork(ctx context.Context, name string) error
	// RemoveNetwork ignores networks that are already gone.
	RemoveNetwork(ctx context.Context, name string) error
	// ListNetworks returns the networks the manager created.
	ListNetworks(ctx context.Context) ([]DockerNetwork, error)
	// ConnectNetwork attaches a container under aliases, unless it is
	// already attached.
	ConnectNetwork(ctx context.Context, name, containerID string, aliases ...string) error
	// DisconnectNetwork ignores a container or network already gone.
	DisconnectNetwork(ctx context.Context, name, containerID string) error
	// SetAliases replaces a container's aliases on a network it is attached
	// to, by reconnecting it: its connections through it are reset.
	SetAliases(ctx context.Context, networkName, id string, aliases []string) error
	PruneNetworks(ctx context.Context, f Filter) (PruneReport, error)
}

type DockerInfo struct {
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

// Image is an image as inspecting or listing it tells.
type Image struct {
	ID          string
	RepoDigests []string
	// Labels and Healthcheck come from the image's config (inspect only);
	// Healthcheck is its HEALTHCHECK's test, nil without one.
	Labels      map[string]string
	Healthcheck []string
	// Created (Unix seconds) and Size come from a listing.
	Created int64
	Size    int64
}

// DockerVolume is a volume on a daemon.
type DockerVolume struct {
	Name      string
	Labels    map[string]string
	CreatedAt string
	// Size is in bytes, -1 when Docker didn't compute it.
	Size int64
}

// DockerNetwork is a network on a daemon.
type DockerNetwork struct {
	Name    string
	Subnets []Subnet
}

type Subnet struct {
	Subnet, Gateway string
}

// PruneReport is what a prune removed.
type PruneReport struct {
	Deleted   int
	Reclaimed uint64
}

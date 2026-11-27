package domain

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
)

// Container is a container as a listing shows it.
type Container struct {
	ID string
	// Names start with "/", as Docker gives them.
	Names   []string
	Image   string
	ImageID string
	Labels  map[string]string
	// State is created, running, paused, restarting, removing, exited or dead.
	State string
	// Status is Docker's human status ("Up 2 hours (healthy)").
	Status string
	// Health is Docker's health status (none, starting, healthy,
	// unhealthy); "" when it gave none.
	Health string
	// Networks are the networks the container is attached to; nil when
	// Docker gave no network settings.
	Networks map[string]ContainerEndpoint
}

// ContainerEndpoint is a container on a network.
type ContainerEndpoint struct {
	// IP is the zero Addr when it has none.
	IP      netip.Addr
	Aliases []string
}

// The container states the manager tells apart.
const (
	StateCreated    = "created"
	StateRunning    = "running"
	StateRestarting = "restarting"
	StatePaused     = "paused"
	StateExited     = "exited"
	StateDead       = "dead"
)

// The healthcheck statuses.
const (
	NoHealthcheck  = "none"
	HealthStarting = "starting"
	Healthy        = "healthy"
	Unhealthy      = "unhealthy"
)

// ContainerInfo is what inspecting a container tells beyond its listing.
type ContainerInfo struct {
	ID string
	// Name starts with "/", as Docker gives it.
	Name         string
	Image        string
	Labels       map[string]string
	RestartCount int
	// State is nil when Docker gave none.
	State *ContainerState
}

type ContainerState struct {
	Status     string
	Running    bool
	Restarting bool
	ExitCode   int
	OOMKilled  bool
	// Health is Docker's health status; "" without a healthcheck.
	Health string
}

// ContainerSpec is a container to create. Empty fields keep the image's or
// Docker's defaults.
type ContainerSpec struct {
	Name  string
	Image string
	// Platform picks the image's platform; nil is the server's.
	Platform   *Platform
	Cmd        []string
	Entrypoint []string
	Env        []string
	User       string
	WorkingDir string
	Labels     map[string]string
	// Healthcheck replaces the image's; nil keeps it.
	Healthcheck *HealthConfig
	// ExposedPorts are port/protocol (80/tcp).
	ExposedPorts []string
	StopSignal   string
	// StopTimeout is how long Docker's own stops wait, in seconds.
	StopTimeout *int
	Tty         bool
	OpenStdin   bool

	// NetworkMode is "" (bridge), host or container:<id>.
	NetworkMode string
	// Networks are attached at creation, with the container's aliases on
	// each.
	Networks map[string][]string
	Mounts   []Mount
	// PortBindings maps an exposed port/protocol to the host addresses
	// publishing it.
	PortBindings map[string][]PortBinding
	Restart      RestartPolicy
	// Log caps the container's logs on disk (the local driver, 3 × 10 MB);
	// false is Docker's default logging.
	Log bool
	// Memory and MemoryReservation are in bytes, NanoCPUs in billionths of
	// a CPU; 0 is unlimited.
	Memory            int64
	MemoryReservation int64
	NanoCPUs          int64
	// ShmSize is /dev/shm's size in bytes; 0 is Docker's 64 MB.
	ShmSize        int64
	Tmpfs          map[string]string
	ReadonlyRootfs bool
	// Init is nil for the daemon's default.
	Init        *bool
	Privileged  bool
	PidMode     string
	CapAdd      []string
	CapDrop     []string
	SecurityOpt []string
	ExtraHosts  []string
	Binds       []string
	Sysctls     map[string]string
	Ulimits     []ContainerUlimit
	Devices     []DeviceMapping
}

// HealthConfig is a container's healthcheck. Test ["NONE"] disables the
// image's.
type HealthConfig struct {
	Test          []string
	Interval      time.Duration
	Timeout       time.Duration
	StartPeriod   time.Duration
	StartInterval time.Duration
	Retries       int
}

// MountType is how a Mount is backed.
type MountType string

const (
	MountVolume MountType = "volume"
	MountBind   MountType = "bind"
)

type Mount struct {
	Type     MountType
	Source   string
	Target   string
	ReadOnly bool
	// Volume is how a volume mount's volume is created when missing; nil
	// for the defaults.
	Volume *VolumeOptions
}

type VolumeOptions struct {
	Labels map[string]string
	// Driver is nil for the local driver's defaults.
	Driver *VolumeDriver
}

type VolumeDriver struct {
	Name    string
	Options map[string]string
}

type PortBinding struct {
	// HostIP is the zero Addr for every address.
	HostIP   netip.Addr
	HostPort string
}

// RestartPolicy is Docker's: Name is "" (never), no, always,
// unless-stopped or on-failure, with MaxRetries for on-failure.
type RestartPolicy struct {
	Name       string
	MaxRetries int
}

type ContainerUlimit struct {
	Name       string
	Soft, Hard int64
}

type DeviceMapping struct {
	PathOnHost, PathInContainer, CgroupPermissions string
}

// ContainerFile is written into a container before it starts.
type ContainerFile struct {
	Path    string // absolute
	Content []byte
	Mode    int64 // 0 is 0644
}

type ExecOptions struct {
	Cmd []string
	Env []string
	// User runs the process as this user (e.g. "0"); empty is the container's.
	User string
	// Stdin, if set, is streamed to the process and then closed.
	Stdin io.Reader
	// Stdout receives the process's stdout; nil discards it.
	Stdout io.Writer
}

// ExecError is returned when the process exits non-zero.
type ExecError struct {
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("exit code %d", e.ExitCode)
	}
	return fmt.Sprintf("exit code %d: %s", e.ExitCode, msg)
}

// LogOptions picks the lines of a container's logs.
type LogOptions struct {
	// Tail is the number of last lines, "" or "all" for every one.
	Tail string
	// Since is an RFC 3339 time or Unix timestamp; "" is the start.
	Since string
	// Follow keeps streaming new lines until ctx ends.
	Follow bool
	// Timestamps prefixes each line with its RFC 3339 time.
	Timestamps bool
}

// TTY is a process on a pseudo-terminal: reads return its output, writes go
// to its input.
type TTY interface {
	io.ReadWriteCloser
	Resize(ctx context.Context, cols, rows uint) error
	// Wait returns the process's exit code once it ends.
	Wait(ctx context.Context) (int, error)
}

// ContainerStats is one reading of a container's counters.
type ContainerStats struct {
	Read time.Time
	// CPU is the total CPU time used, in nanoseconds.
	CPU uint64
	// MemoryUsage is in bytes, page cache included; MemoryStats are the
	// cgroup's counters (inactive_file…).
	MemoryUsage uint64
	MemoryStats map[string]uint64
	// RxBytes and TxBytes are summed over the container's networks.
	RxBytes, TxBytes uint64
}

// ContainerEvent is a container's event (die, destroy, oom…).
type ContainerEvent struct {
	Action string
	// Attributes are the container's labels, its name and image.
	Attributes map[string]string
}

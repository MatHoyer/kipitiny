package core

import (
	"fmt"
	"maps"
	"net"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxRuntimeEntries = 50
	maxShmSizeMB      = 64 << 10
)

var (
	stopSignalRe = regexp.MustCompile(`^(SIG)?[A-Z][A-Z0-9+-]*$|^[0-9]{1,2}$`)
	sysctlRe     = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_-]+)+$`)
	labelKeyRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	capRe        = regexp.MustCompile(`^[A-Z][A-Z_]*$`)
	ulimitNames  = []string{"core", "cpu", "data", "fsize", "locks", "memlock", "msgqueue", "nice", "nofile", "nproc", "rss", "rtprio", "rttime", "sigpending", "stack"}
)

// normalizeRuntime trims r and drops its empty collections, so equal
// settings compare equal however they were sent.
func normalizeRuntime(r store.Runtime) store.Runtime {
	r.User, r.WorkingDir, r.StopSignal = strings.TrimSpace(r.User), strings.TrimSpace(r.WorkingDir), strings.TrimSpace(r.StopSignal)
	if len(r.Command) == 0 {
		r.Command = nil
	}
	if len(r.Entrypoint) == 0 {
		r.Entrypoint = nil
	}
	if len(r.ExtraHosts) == 0 {
		r.ExtraHosts = nil
	}
	if len(r.Labels) == 0 {
		r.Labels = nil
	}
	if len(r.Tmpfs) == 0 {
		r.Tmpfs = nil
	}
	if len(r.Ulimits) == 0 {
		r.Ulimits = nil
	}
	if len(r.Sysctls) == 0 {
		r.Sysctls = nil
	}
	caps := func(l []string) []string {
		var out []string
		for _, c := range l {
			out = append(out, strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_"))
		}
		return out
	}
	r.CapAdd, r.CapDrop = caps(r.CapAdd), caps(r.CapDrop)
	if len(r.Devices) == 0 {
		r.Devices = nil
	}
	if len(r.SecurityOpt) == 0 {
		r.SecurityOpt = nil
	}
	if len(r.Binds) == 0 {
		r.Binds = nil
	}
	return r
}

func validateRuntime(s store.Service) error {
	r := s.Runtime
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
	}
	if len(r.Command) > maxRuntimeEntries || len(r.Entrypoint) > maxRuntimeEntries || len(r.Labels) > maxRuntimeEntries ||
		len(r.Tmpfs) > 10 || len(r.Sysctls) > maxRuntimeEntries || len(r.ExtraHosts) > maxRuntimeEntries {
		return invalid("too many runtime entries")
	}
	if strings.ContainsAny(r.User, " \t\n") {
		return invalid("user must be a name or UID, optionally with :group")
	}
	if r.WorkingDir != "" && !path.IsAbs(r.WorkingDir) {
		return invalid("working directory must be an absolute path")
	}
	for k := range r.Labels {
		lower := strings.ToLower(k)
		if !labelKeyRe.MatchString(k) {
			return invalid("invalid label %q", k)
		}
		if strings.HasPrefix(lower, "kipitiny.") || strings.HasPrefix(lower, "traefik.") {
			return invalid("labels starting with kipitiny. or traefik. are the manager's")
		}
	}
	for p, opts := range r.Tmpfs {
		if !path.IsAbs(p) || path.Clean(p) == "/" || strings.ContainsAny(p, ",:") {
			return invalid("tmpfs path %q must be an absolute path", p)
		}
		if strings.ContainsAny(opts, " \n") {
			return invalid("tmpfs %s: invalid options %q", p, opts)
		}
		if slices.ContainsFunc(s.Volumes, func(v store.Volume) bool { return path.Clean(v.Path) == path.Clean(p) }) {
			return invalid("tmpfs %s is also a volume's path", p)
		}
	}
	if r.ShmSizeMB < 0 || r.ShmSizeMB > maxShmSizeMB {
		return invalid("shm size must be between 0 and %d MB", maxShmSizeMB)
	}
	for name, u := range r.Ulimits {
		if !slices.Contains(ulimitNames, name) {
			return invalid("unknown ulimit %q", name)
		}
		if u.Soft < -1 || u.Hard < -1 || (u.Hard != -1 && (u.Soft == -1 || u.Soft > u.Hard)) {
			return invalid("ulimit %s: soft must be at most hard (-1 is unlimited)", name)
		}
	}
	for k := range r.Sysctls {
		if !sysctlRe.MatchString(k) {
			return invalid("invalid sysctl %q", k)
		}
		if s.HostNetwork && strings.HasPrefix(k, "net.") {
			return invalid("an app in the host network can't set net.* sysctls")
		}
	}
	if r.StopSignal != "" && !stopSignalRe.MatchString(r.StopSignal) {
		return invalid("stop signal must be a signal name (SIGINT) or number")
	}
	for _, c := range append(slices.Clone(r.CapAdd), r.CapDrop...) {
		if !capRe.MatchString(c) {
			return invalid("invalid capability %q", c)
		}
	}
	for _, d := range r.Devices {
		parts := strings.Split(d, ":")
		perms := ""
		if len(parts) == 3 {
			perms = parts[2]
		}
		if len(parts) > 3 || !path.IsAbs(parts[0]) || (len(parts) > 1 && !path.IsAbs(parts[1])) ||
			strings.Trim(perms, "rwm") != "" {
			return invalid("device %q must be /dev/host[:/dev/container[:rwm]]", d)
		}
	}
	for _, b := range r.Binds {
		parts := strings.Split(b, ":")
		if len(parts) < 2 || len(parts) > 3 || !path.IsAbs(parts[0]) || !path.IsAbs(parts[1]) || (len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw") {
			return invalid("bind mount %q must be /host/path:/container/path[:ro]", b)
		}
	}
	if len(r.Binds) > maxRuntimeEntries {
		return invalid("too many bind mounts")
	}
	for _, o := range r.SecurityOpt {
		if o == "" || strings.ContainsAny(o, " \t\n") {
			return invalid("invalid security option %q", o)
		}
	}
	for _, h := range r.ExtraHosts {
		host, ip, ok := strings.Cut(h, ":")
		if !ok || host == "" || strings.ContainsAny(host, " \t") || (ip != "host-gateway" && net.ParseIP(ip) == nil) {
			return invalid("extra host %q must be host:ip (or host:host-gateway)", h)
		}
	}
	return nil
}

// applyRuntime sets an app's runtime settings on its container.
func applyRuntime(cfg *container.Config, host *container.HostConfig, r store.Runtime) {
	if len(r.Command) > 0 {
		cfg.Cmd = r.Command
	}
	if len(r.Entrypoint) > 0 {
		cfg.Entrypoint = r.Entrypoint
	}
	cfg.User, cfg.WorkingDir, cfg.StopSignal = r.User, r.WorkingDir, r.StopSignal
	cfg.Tty, cfg.OpenStdin = r.TTY, r.StdinOpen
	for k, v := range r.Labels {
		if _, ours := cfg.Labels[k]; !ours {
			cfg.Labels[k] = v
		}
	}
	host.Tmpfs = maps.Clone(r.Tmpfs)
	host.ShmSize = int64(r.ShmSizeMB) << 20
	for _, name := range slices.Sorted(maps.Keys(r.Ulimits)) {
		u := r.Ulimits[name]
		host.Ulimits = append(host.Ulimits, &container.Ulimit{Name: name, Soft: u.Soft, Hard: u.Hard})
	}
	host.Sysctls = maps.Clone(r.Sysctls)
	host.ReadonlyRootfs = r.ReadOnly
	if r.Init {
		host.Init = &r.Init
	}
	host.ExtraHosts = slices.Clone(r.ExtraHosts)
	host.CapAdd = slices.Clone(r.CapAdd)
	if slices.Contains(r.CapAdd, "NET_RAW") || slices.Contains(r.CapAdd, "ALL") || r.Privileged {
		host.CapDrop = slices.DeleteFunc(host.CapDrop, func(c string) bool { return c == "NET_RAW" })
	}
	for _, c := range r.CapDrop {
		if !slices.Contains(host.CapDrop, c) {
			host.CapDrop = append(host.CapDrop, c)
		}
	}
	host.Privileged = r.Privileged
	host.Binds = slices.Clone(r.Binds)
	host.SecurityOpt = slices.Clone(r.SecurityOpt)
	for _, d := range r.Devices {
		parts := append(strings.Split(d, ":"), "", "")
		m := container.DeviceMapping{PathOnHost: parts[0], PathInContainer: parts[1], CgroupPermissions: parts[2]}
		if m.PathInContainer == "" {
			m.PathInContainer = m.PathOnHost
		}
		if m.CgroupPermissions == "" {
			m.CgroupPermissions = "rwm"
		}
		host.Devices = append(host.Devices, m)
	}
}

// runtimeHostAccess is the part of r only admins change.
func runtimeHostAccess(r store.Runtime) store.Runtime {
	return store.Runtime{CapAdd: r.CapAdd, Devices: r.Devices, Privileged: r.Privileged, SecurityOpt: r.SecurityOpt, Binds: r.Binds}
}

package domain

// Healthcheck is a Docker healthcheck, as compose's healthcheck key writes
// it. Test ["NONE"] disables the image's; an empty Test means none set.
type Healthcheck struct {
	// Test is ["CMD", args...], ["CMD-SHELL", command] or ["NONE"].
	Test               []string `json:"test,omitempty"`
	IntervalSeconds    int      `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int      `json:"timeoutSeconds,omitempty"`
	StartPeriodSeconds int      `json:"startPeriodSeconds,omitempty"`
	Retries            int      `json:"retries,omitempty"`
}

// Set reports whether the healthcheck replaces the image's.
func (h Healthcheck) Set() bool { return len(h.Test) > 0 }

// Runtime is how an app's container runs, beyond its image, env and
// resources: compose's command, entrypoint, user, tmpfs… Empty fields keep
// the image's or Docker's defaults.
type Runtime struct {
	// Command and Entrypoint replace the image's CMD and ENTRYPOINT;
	// Entrypoint [""] clears the image's.
	Command    []string `json:"command,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	// User is a name or UID, optionally with :group.
	User       string `json:"user,omitempty"`
	WorkingDir string `json:"workingDir,omitempty"`
	// Labels are added to the container's own (never kipitiny.* or traefik.*).
	Labels map[string]string `json:"labels,omitempty"`
	// Tmpfs maps an in-memory mount's path to its options (size=64m,mode=1777).
	Tmpfs map[string]string `json:"tmpfs,omitempty"`
	// ShmSizeMB sizes /dev/shm; 0 keeps Docker's 64 MB.
	ShmSizeMB int               `json:"shmSizeMb,omitempty"`
	Ulimits   map[string]Ulimit `json:"ulimits,omitempty"`
	// Sysctls are namespaced kernel parameters (net.core.somaxconn…).
	Sysctls map[string]string `json:"sysctls,omitempty"`
	// ReadOnly mounts the root filesystem read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Init runs an init process as PID 1 that reaps zombies.
	Init       bool   `json:"init,omitempty"`
	StopSignal string `json:"stopSignal,omitempty"`
	TTY        bool   `json:"tty,omitempty"`
	StdinOpen  bool   `json:"stdinOpen,omitempty"`
	// ExtraHosts are host:ip entries added to /etc/hosts (ip may be
	// host-gateway).
	ExtraHosts []string `json:"extraHosts,omitempty"`
	// The settings below loosen the container's isolation: only admins set
	// them (HostAccess).
	//
	// CapAdd and CapDrop are Linux capabilities without CAP_ (NET_ADMIN).
	CapAdd  []string `json:"capAdd,omitempty"`
	CapDrop []string `json:"capDrop,omitempty"`
	// Devices are host devices, as host[:container[:permissions]].
	Devices []string `json:"devices,omitempty"`
	// Privileged gives the container every capability and device.
	Privileged  bool     `json:"privileged,omitempty"`
	SecurityOpt []string `json:"securityOpt,omitempty"`
	// Binds mount host paths, as /host:/container[:ro].
	Binds []string `json:"binds,omitempty"`
	// MemoryReservationMB is the memory Docker tries to leave the container
	// when the server runs short (a soft limit).
	MemoryReservationMB int `json:"memoryReservationMb,omitempty"`
	// Restart is "" (always restarted), "no" (a job: it runs once, a
	// deploy waits for it to exit 0) or "on-failure[:max retries]".
	Restart string `json:"restart,omitempty"`
	// Platform is the image platform pulled and run (linux/arm64,
	// linux/arm/v7); "" is the server's own. Another architecture runs
	// under emulation.
	Platform string `json:"platform,omitempty"`
}

// Ulimit is a soft and hard limit; compose's single value sets both.
type Ulimit struct {
	Soft int64 `json:"soft"`
	Hard int64 `json:"hard"`
}

// IsZero reports whether r changes nothing.
func (r Runtime) IsZero() bool {
	return len(r.Command) == 0 && len(r.Entrypoint) == 0 && r.User == "" && r.WorkingDir == "" && len(r.Labels) == 0 &&
		len(r.Tmpfs) == 0 && r.ShmSizeMB == 0 && len(r.Ulimits) == 0 && len(r.Sysctls) == 0 && !r.ReadOnly && !r.Init &&
		r.StopSignal == "" && !r.TTY && !r.StdinOpen && len(r.ExtraHosts) == 0 && len(r.CapDrop) == 0 && r.Restart == "" && r.MemoryReservationMB == 0 && r.Platform == "" && !r.HostAccess()
}

// HostAccess reports whether r loosens the container's isolation, which
// gives control of the server, like the Docker socket.
func (r Runtime) HostAccess() bool {
	return len(r.CapAdd) > 0 || len(r.Devices) > 0 || r.Privileged || len(r.SecurityOpt) > 0 || len(r.Binds) > 0
}

// Middlewares are the Traefik middlewares on an app's router; the zero
// value adds none.
type Middlewares struct {
	// BasicAuth asks for one of these users' credentials.
	BasicAuth []BasicAuthUser `json:"basicAuth,omitempty"`
	// IPAllowList limits clients to these IPs or CIDR ranges.
	IPAllowList []string `json:"ipAllowList,omitempty"`
	// RateLimit caps requests per client IP; nil means unlimited.
	RateLimit *RateLimit `json:"rateLimit,omitempty"`
	// Headers are set on every response; an empty value removes the header.
	Headers map[string]string `json:"headers,omitempty"`
}

// BasicAuthUser is one basic auth login. Its password is either stored as
// Hash or read from a password manager at deploy (Ref).
type BasicAuthUser struct {
	Name string `json:"name"`
	// Hash is the bcrypt hash of the password. For a Ref user it is only
	// set in deployment snapshots, hashed from the value fetched at deploy.
	Hash string `json:"hash,omitempty"`
	// Ref is a password manager reference, {{ scheme://… }}.
	Ref string `json:"ref,omitempty"`
}

// RateLimit allows Average requests per second per client IP, with bursts
// of up to Burst requests.
type RateLimit struct {
	Average int `json:"average"`
	Burst   int `json:"burst"`
}

// Maintenance is a project's maintenance page, served by the manager through
// Traefik when one of its apps has no replica to answer, and maintenance
// mode, which serves it to every visitor of its apps while the replicas
// keep running.
type Maintenance struct {
	// Enabled is maintenance mode.
	Enabled bool `json:"enabled"`
	// AllowIPs still reach the apps in maintenance mode (IPs or CIDR ranges).
	AllowIPs []string `json:"allowIps,omitempty"`
	// Title and Message replace the default page's text; empty keeps it.
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
	// HTML, when set, is served instead of the default page.
	HTML string `json:"html,omitempty"`
	// Starting is the page visitors get while the project wakes from sleep.
	Starting StartingPage `json:"starting,omitzero"`
}

// StartingPage replaces the default "Starting…" page's text, or the whole
// page with HTML; zero keeps the default.
type StartingPage struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
	HTML    string `json:"html,omitempty"`
}

// IsZero reports whether no middleware is configured.
func (m Middlewares) IsZero() bool {
	return len(m.BasicAuth) == 0 && len(m.IPAllowList) == 0 && m.RateLimit == nil && len(m.Headers) == 0
}
